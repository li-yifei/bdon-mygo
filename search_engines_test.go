package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSearchEnginePersistenceAndFailedSwitch(t *testing.T) {
	root := t.TempDir()
	bundled := &rustDeck{}
	caps := cstring(`{"supportedGoals":[]}`)
	custom := &rustDeck{capabilities: func() *byte { return &caps[0] }, stringLen: func(*byte) uintptr { return uintptr(len(caps) - 1) }, free: func(*byte) {}}
	native := &nativeDeck{rust: bundled}
	e := newSearchEngines(root, native, bundled)
	loads := 0
	e.load = func(string) (*rustDeck, error) { loads++; return custom, nil }
	source := filepath.Join(t.TempDir(), "multithread"+nativeLibrarySuffix())
	if err := os.WriteFile(source, []byte("experimental test fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := e.importLibrary(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.selectEngine(config, true); err != nil {
		t.Fatal(err)
	}
	if native.engine() != custom {
		t.Fatal("custom engine was not selected")
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	next := newSearchEngines(root, native, bundled)
	next.load = e.load
	if err = next.restore(); err != nil {
		t.Fatal(err)
	}
	if next.current() != config {
		t.Fatal("choice not restored after source removed")
	}
	bad := config
	bad.Library = "../../bad" + nativeLibrarySuffix()
	if err = next.selectEngine(bad, true); err == nil {
		t.Fatal("accepted external path")
	}
	if next.current() != config || native.engine() != custom {
		t.Fatal("failed selection changed active engine")
	}
	library := filepath.Join(root, "engines", config.Library)
	if err = os.WriteFile(library, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = next.selectEngine(config, true); err == nil {
		t.Fatal("accepted modified library")
	}
	if err = next.selectEngine(searchEngineConfig{Mode: "bundled"}, true); err != nil {
		t.Fatal(err)
	}
	if native.engine() != bundled || next.current().Library != "" {
		t.Fatal("bundled engine not restored")
	}
	if loads != 2 {
		t.Fatalf("unexpected number of loads: %d", loads)
	}
}
func TestCustomEngineABIValidationLive(t *testing.T) {
	if os.Getenv("OURNOTES_NATIVE_TEST_DATA") == "" {
		t.Skip("opt-in FFI test")
	}
	bundled, err := loadRust(defaultFFILib())
	if err != nil {
		t.Fatal(err)
	}
	native := &nativeDeck{rust: bundled}
	e := newSearchEngines(t.TempDir(), native, bundled)
	c, err := e.importLibrary(defaultFFILib())
	if err != nil {
		t.Fatal(err)
	}
	if err = e.selectEngine(c, true); err != nil {
		t.Fatal(err)
	}
	if err = e.restore(); err != nil {
		t.Fatal(err)
	}
	if e.current().Mode != "custom" {
		t.Fatal("custom engine not restored")
	}
	if runtime.GOOS == "darwin" {
		if _, err = loadRust("/usr/lib/libSystem.B.dylib"); err == nil {
			t.Fatal("incompatible library accepted")
		}
	}
}

// A release ships one engine per platform: the experimental algorithm then
// imports the bundled engine, keeping the platform suffix on the copy.
func TestExperimentalConfigUsesBundledEngine(t *testing.T) {
	t.Chdir(t.TempDir())
	engine := filepath.Join(t.TempDir(), nativeEngineName())
	if err := os.WriteFile(engine, []byte("bundled engine fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OURNOTES_FFI_LIB", engine)
	e := newSearchEngines(t.TempDir(), &nativeDeck{}, &rustDeck{})
	config, err := e.experimentalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != "custom" || config.Name != nativeEngineName() || !strings.HasSuffix(config.Library, nativeLibrarySuffix()) || len(config.Library) != 64+len(nativeLibrarySuffix()) {
		t.Fatalf("unexpected config: %+v", config)
	}
	if _, err := os.Stat(filepath.Join(e.root, "engines", config.Library)); err != nil {
		t.Fatal(err)
	}
	other := ".so"
	if nativeLibrarySuffix() == ".so" {
		other = ".dll"
	}
	foreign := filepath.Join(t.TempDir(), "libournotes_ffi"+other)
	if err := os.WriteFile(foreign, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.importLibrary(foreign); err == nil {
		t.Fatal("accepted a library of another platform")
	}
}

func TestNativeLibrarySuffix(t *testing.T) {
	want := map[string]string{"darwin": ".dylib", "windows": ".dll", "linux": ".so"}[runtime.GOOS]
	if want != "" && nativeLibrarySuffix() != want {
		t.Fatalf("suffix %q, want %q", nativeLibrarySuffix(), want)
	}
}
