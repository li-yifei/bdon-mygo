package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

type searchEngineConfig struct {
	Mode    string `json:"mode"`
	Library string `json:"library,omitempty"`
	Name    string `json:"name,omitempty"`
}

// Only native UI can change this configuration; the remote page receives no
// filesystem or library-loading binding. Loaded libraries stay alive while any
// concurrent FFI invocation can still hold their function pointers.
type searchEngines struct {
	mu   sync.Mutex
	root string // data: imported engines in engines/
	// configRoot holds search-engine.json, the choice; root when empty.
	configRoot string
	native     *nativeDeck
	bundled    *rustDeck
	config     searchEngineConfig
	loaded     map[string]*rustDeck
	load       func(string) (*rustDeck, error)
}

func newSearchEngines(root string, native *nativeDeck, bundled *rustDeck) *searchEngines {
	return &searchEngines{root: root, native: native, bundled: bundled, config: searchEngineConfig{Mode: "bundled"}, loaded: map[string]*rustDeck{}, load: loadRust}
}
func (e *searchEngines) choicePath() string {
	if e.configRoot != "" {
		return filepath.Join(e.configRoot, "search-engine.json")
	}
	return filepath.Join(e.root, "search-engine.json")
}
func (e *searchEngines) current() searchEngineConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.config
}
func (e *searchEngines) restore() error {
	b, err := os.ReadFile(e.choicePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var config searchEngineConfig
	if err = json.Unmarshal(b, &config); err != nil {
		return err
	}
	return e.selectEngine(config, false)
}

// The experimental replacement algorithm runs the native engine in place of
// the page's web worker. A development checkout may supply a separate
// experimental build (libournotes_ffi_experimental.<ext>); a release ships one
// engine per platform, and the bundled engine in the app's resources is then
// the experimental one. The product UI presents an algorithm choice without
// library or ABI configuration.
func (e *searchEngines) experimentalConfig() (searchEngineConfig, error) {
	current := e.current()
	suffix := nativeLibrarySuffix()
	var candidates []string
	for _, dir := range nativeLibraryDirs() {
		candidates = append(candidates, filepath.Join(dir, "libournotes_ffi_experimental"+suffix))
	}
	candidates = append(candidates, filepath.Join(e.root, "engines", "experimental"+suffix))
	if bundled := defaultFFILib(); bundled != "" {
		candidates = append(candidates, bundled)
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return e.importLibrary(path)
		}
	}
	if current.Mode == "custom" {
		return current, nil
	}
	return searchEngineConfig{}, i18n.New("engine.error.notReady")
}

func (e *searchEngines) importLibrary(path string) (searchEngineConfig, error) {
	// Imported copies are named <sha256><suffix> under engines/, keeping the
	// platform suffix (.dylib, .so or .dll) the system loader expects.
	if !strings.EqualFold(filepath.Ext(path), nativeLibrarySuffix()) {
		return searchEngineConfig{}, i18n.New("engine.error.wrongPlatform")
	}
	info, err := os.Stat(path)
	if err != nil {
		return searchEngineConfig{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return searchEngineConfig{}, i18n.New("engine.error.badLibrary")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return searchEngineConfig{}, err
	}
	sum := sha256.Sum256(b)
	name := hex.EncodeToString(sum[:]) + nativeLibrarySuffix()
	dir := filepath.Join(e.root, "engines")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return searchEngineConfig{}, err
	}
	target := filepath.Join(dir, name)
	existing, readErr := os.ReadFile(target)
	if readErr != nil || sha256.Sum256(existing) != sum {
		if err := writePrivateFile(target, b); err != nil {
			return searchEngineConfig{}, err
		}
	}
	return searchEngineConfig{Mode: "custom", Library: name, Name: filepath.Base(path)}, nil
}
func (e *searchEngines) selectEngine(config searchEngineConfig, persist bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var rust *rustDeck
	switch config.Mode {
	case "bundled":
		config = searchEngineConfig{Mode: "bundled"}
		rust = e.bundled
	case "custom":
		suffix := nativeLibrarySuffix()
		if len(config.Library) != 64+len(suffix) || filepath.Base(config.Library) != config.Library || !strings.HasSuffix(config.Library, suffix) {
			return i18n.New("engine.error.badPath")
		}
		digest, err := hex.DecodeString(strings.TrimSuffix(config.Library, suffix))
		if err != nil || len(digest) != 32 {
			return i18n.New("engine.error.badIdentity")
		}
		path := filepath.Join(e.root, "engines", config.Library)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:])+suffix != config.Library {
			return i18n.New("engine.error.checksum")
		}
		rust = e.loaded[config.Library]
		if rust == nil {
			rust, err = e.load(path)
			if err != nil {
				return i18n.Wrap(err, "engine.error.load")
			}
			caps := rust.take(rust.capabilities())
			var obj map[string]json.RawMessage
			if json.Unmarshal([]byte(caps), &obj) != nil || obj == nil || obj["error"] != nil {
				return i18n.New("engine.error.badCapabilities")
			}
			e.loaded[config.Library] = rust
		}
	default:
		return i18n.New("engine.error.unknown")
	}
	if persist {
		b, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return err
		}
		if err = writePrivateFile(e.choicePath(), b); err != nil {
			return err
		}
	}
	e.native.mu.Lock()
	e.native.rust = rust
	e.native.nativeSolver = config.Mode == "custom"
	e.native.mu.Unlock()
	e.config = config
	return nil
}
func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
