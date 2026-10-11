package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDevelopmentStorageSurvivesBundleReplacement(t *testing.T) {
	t.Setenv("MYGO_ENV", "development")
	t.Setenv("OURNOTES_DEV_STORAGE", "/project/.local-storage")
	if got := defaultResourceDir(); got != "/project/.local-storage" {
		t.Fatalf("dev storage = %q", got)
	}
	t.Setenv("MYGO_ENV", "production")
	if got := defaultResourceDir(); got == "/project/.local-storage" {
		t.Fatal("production used development override")
	}
}

func TestReleaseStorageIsPerUser(t *testing.T) {
	t.Setenv("MYGO_ENV", "production")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	want := filepath.Join(home, "Library", "Application Support", product.ConfigDir)
	if runtime.GOOS == "linux" {
		want = filepath.Join(home, "data", product.ConfigDir)
	}
	if got := defaultResourceDir(); runtime.GOOS != "windows" && got != want {
		t.Fatalf("release storage = %q, want %q", got, want)
	}
}

func TestLegacyStorageDirs(t *testing.T) {
	exe := "/Volumes/X/" + product.Name + ".app/Contents/MacOS/" + product.Name
	got := legacyStorageDirs(exe, "/Users/u")
	want := []string{
		"/Volumes/X/" + product.Name + ".app/Contents/Resources/" + product.LegacyStorageDir,
		"/Volumes/X/" + product.LegacyName + ".app/Contents/Resources/" + product.LegacyStorageDir,
		"/Applications/" + product.LegacyName + ".app/Contents/Resources/" + product.LegacyStorageDir,
		"/Users/u/Applications/" + product.LegacyName + ".app/Contents/Resources/" + product.LegacyStorageDir,
	}
	if len(got) != len(want) {
		t.Fatalf("legacy dirs = %q", got)
	}
	for i := range want {
		if got[i] != filepath.Clean(want[i]) {
			t.Fatalf("legacy dir %d = %q, want %q", i, got[i], want[i])
		}
	}
	if got := legacyStorageDirs("/opt/app/"+product.LinuxBinary, ""); len(got) != 1 || got[0] != "/opt/app/"+product.DevStorageDir {
		t.Fatalf("linux legacy dirs = %q", got)
	}
}

func TestMigrateLegacyStorage(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "old", "storage")
	if err := os.MkdirAll(filepath.Join(legacy, "android", "account"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "android", "account", "_player"), []byte("save"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "search-engine.json"), []byte(`{"mode":"bundled"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "Application Support", product.ConfigDir)
	if err := os.MkdirAll(dest, 0o700); err != nil { // present but empty
		t.Fatal(err)
	}
	from, err := migrateLegacyStorage(dest, []string{filepath.Join(root, "missing"), legacy})
	if err != nil || from != legacy {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "android", "account", "_player"))
	if err != nil || string(data) != "save" {
		t.Fatalf("migrated save = %q, %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(dest, "search-engine.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("migrated file mode: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "search-engine.json")); err != nil {
		t.Fatal("legacy storage must stay in place")
	}
	// A non-empty destination is never overwritten.
	if err := os.WriteFile(filepath.Join(legacy, "search-engine.json"), []byte(`{"mode":"custom"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if from, err := migrateLegacyStorage(dest, []string{legacy}); err != nil || from != "" {
		t.Fatalf("second migrate = %q, %v", from, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "search-engine.json")); string(data) != `{"mode":"bundled"}` {
		t.Fatalf("destination overwritten: %s", data)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".*migrating*")); len(leftovers) != 0 {
		t.Fatalf("staging left behind: %q", leftovers)
	}
}

func TestMigrateLegacyStorageMissingDestination(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	if err := os.MkdirAll(filepath.Join(legacy, "datasets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "datasets", "a.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "new", "bdon")
	if from, err := migrateLegacyStorage(dest, []string{legacy}); err != nil || from != legacy {
		t.Fatalf("migrate = %q, %v", from, err)
	}
	if !isFile(filepath.Join(dest, "datasets", "a.json")) {
		t.Fatal("dataset not migrated")
	}
	empty := filepath.Join(root, "empty-dest")
	if from, err := migrateLegacyStorage(empty, []string{filepath.Join(root, "nothing")}); err != nil || from != "" {
		t.Fatalf("no legacy = %q, %v", from, err)
	}
}
