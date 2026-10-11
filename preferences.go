package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// appPreferences is preferences.json in the product configuration directory.
// Fields this version does not know are kept when it writes the file.
type appPreferences struct {
	ResourceDir string `json:"resourceDir,omitempty"`
	// Language is "system" (or empty) to follow the system language, or one
	// of zh-Hans, zh-Hant, ja, en.
	Language string `json:"language,omitempty"`
	// PlatformToolsTermsAccepted records the user's agreement to the Android
	// SDK License Agreement before the app downloads Google's Platform-Tools.
	PlatformToolsTermsAccepted bool `json:"platformToolsTermsAccepted,omitempty"`
}

var preferencesMu sync.Mutex

// preferencesFile is the path of preferences.json; tests point it elsewhere.
var preferencesFile = func() string {
	p, err := productConfigPath("preferences.json")
	if err != nil {
		return ""
	}
	return p
}

func preferencesPath() string { return preferencesFile() }

// readPreferenceFields returns the raw fields of preferences.json.
func readPreferenceFields(path string) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &fields) != nil {
		return map[string]json.RawMessage{}
	}
	return fields
}

func loadPreferences() appPreferences {
	var prefs appPreferences
	p := preferencesPath()
	if p == "" {
		return prefs
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &prefs)
	}
	return prefs
}

// updatePreferences applies change to the stored preferences and writes the
// file, keeping unknown fields.
func updatePreferences(change func(*appPreferences)) error {
	preferencesMu.Lock()
	defer preferencesMu.Unlock()
	p := preferencesPath()
	if p == "" {
		return nil
	}
	fields := readPreferenceFields(p)
	prefs := loadPreferences()
	change(&prefs)
	known, _ := json.Marshal(prefs)
	delete(fields, "resourceDir")
	delete(fields, "language")
	_ = json.Unmarshal(known, &fields)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

func persistedResourceDir() string {
	dir := loadPreferences().ResourceDir
	if info, err := os.Stat(dir); dir != "" && err == nil && info.IsDir() {
		return filepath.Clean(dir)
	}
	return ""
}

func persistResourceDir(path string) error {
	return updatePreferences(func(p *appPreferences) { p.ResourceDir = filepath.Clean(path) })
}

// persistedLanguage returns the stored language preference, "system" by
// default.
func persistedLanguage() string {
	return i18n.NormalizePreference(loadPreferences().Language)
}

// applyLanguagePreference stores a language preference and switches the
// native interface to it; the i18n change listeners re-render the windows
// and rebuild the menu.
func applyLanguagePreference(pref string) error {
	pref = i18n.NormalizePreference(pref)
	if err := updatePreferences(func(p *appPreferences) { p.Language = pref }); err != nil {
		return i18n.Wrap(err, "settings.error.savePreferences")
	}
	i18n.SetPreference(pref)
	return nil
}
