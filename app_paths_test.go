package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testPathEnv(goos string, vars map[string]string, dirs, files []string) pathEnv {
	has := func(list []string) func(string) bool {
		return func(p string) bool {
			for _, q := range list {
				if filepath.Clean(q) == filepath.Clean(p) {
					return true
				}
			}
			return false
		}
	}
	return pathEnv{goos: goos, home: "/home/u", exeDir: "/opt/app", cwd: "/src/app", getenv: func(k string) string { return vars[k] }, isDir: has(dirs), isFile: has(files)}
}

func TestResolveAppDirsPerPlatform(t *testing.T) {
	n := product.ConfigDir
	j := filepath.Join
	cases := []struct {
		name string
		env  pathEnv
		want appDirs
	}{
		{"darwin", testPathEnv("darwin", nil, []string{"/opt/app/data"}, nil), appDirs{
			Config: j("/home/u/Library/Application Support", n), Data: j("/home/u/Library/Application Support", n),
			Cache: j("/home/u/Library/Caches", n), Logs: j("/home/u/Library/Logs", n)}},
		{"windows", testPathEnv("windows", map[string]string{"APPDATA": "/w/Roaming", "LOCALAPPDATA": "/w/Local"}, nil, nil), appDirs{
			Config: j("/w/Roaming", n), Data: j("/w/Local", n, "Data"), Cache: j("/w/Local", n, "Cache"), Logs: j("/w/Local", n, "Logs")}},
		{"windows defaults", testPathEnv("windows", nil, nil, nil), appDirs{
			Config: j("/home/u/AppData/Roaming", n), Data: j("/home/u/AppData/Local", n, "Data"), Cache: j("/home/u/AppData/Local", n, "Cache"), Logs: j("/home/u/AppData/Local", n, "Logs")}},
		{"linux defaults", testPathEnv("linux", map[string]string{"XDG_DATA_HOME": "relative/ignored"}, nil, nil), appDirs{
			Config: j("/home/u/.config", n), Data: j("/home/u/.local/share", n), Cache: j("/home/u/.cache", n), Logs: j("/home/u/.local/state", n)}},
		{"linux xdg", testPathEnv("linux", map[string]string{"XDG_CONFIG_HOME": "/x/c", "XDG_DATA_HOME": "/x/d", "XDG_CACHE_HOME": "/x/k", "XDG_STATE_HOME": "/x/s"}, nil, nil), appDirs{
			Config: j("/x/c", n), Data: j("/x/d", n), Cache: j("/x/k", n), Logs: j("/x/s", n)}},
		{"windows portable", testPathEnv("windows", nil, []string{"/opt/app/data"}, nil), appDirs{
			Config: "/opt/app/data/config", Data: "/opt/app/data/data", Cache: "/opt/app/data/cache", Logs: "/opt/app/data/logs", Portable: true}},
		{"linux portable marker", testPathEnv("linux", nil, nil, []string{"/opt/app/portable"}), appDirs{
			Config: "/opt/app/data/config", Data: "/opt/app/data/data", Cache: "/opt/app/data/cache", Logs: "/opt/app/data/logs", Portable: true}},
		{"development", testPathEnv("darwin", map[string]string{"MYGO_ENV": "development"}, nil, nil), appDirs{
			Config: "/src/app/.local-storage/config", Data: "/src/app/.local-storage", Cache: "/src/app/.local-storage/cache", Logs: "/src/app/.local-storage/logs", Development: true}},
		{"development override", testPathEnv("linux", map[string]string{"MYGO_ENV": "development", "OURNOTES_DEV_STORAGE": "/dev/store"}, []string{"/opt/app/data"}, nil), appDirs{
			Config: "/dev/store/config", Data: "/dev/store", Cache: "/dev/store/cache", Logs: "/dev/store/logs", Development: true}},
	}
	for _, c := range cases {
		if got := resolveAppDirs(c.env); got != c.want {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

func TestMigrateAppLayoutMovesOnlyRecreatableFiles(t *testing.T) {
	root := t.TempDir()
	support := filepath.Join(root, "support")
	d := appDirs{Config: support, Data: support, Cache: filepath.Join(root, "cache"), Logs: filepath.Join(root, "logs")}
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(support, "datasets", "a.json"))
	write(filepath.Join(support, "tools", "platform-tools", "adb"))
	write(filepath.Join(support, ".event-pull-1", "Master", "x"))
	write(filepath.Join(support, "android", "account", "player.json"))
	write(filepath.Join(support, "engines", "e.dylib"))
	write(filepath.Join(support, "search-engine.json"))
	write(filepath.Join(support, "preferences.json"))
	moved, errs := migrateAppLayout(d)
	if len(errs) != 0 || len(moved) != 3 {
		t.Fatalf("moved %v, errors %v", moved, errs)
	}
	for _, p := range []string{"cache/datasets/a.json", "cache/tools/platform-tools/adb", "cache/.event-pull-1/Master/x", "support/android/account/player.json", "support/engines/e.dylib", "support/search-engine.json", "support/preferences.json"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	// A second run, or a destination that exists, moves nothing.
	write(filepath.Join(support, "datasets", "b.json"))
	if moved, _ := migrateAppLayout(d); len(moved) != 0 {
		t.Fatalf("moved again: %v", moved)
	}
	if _, err := os.Stat(filepath.Join(support, "datasets", "b.json")); err != nil {
		t.Fatal("source with an existing destination must stay")
	}
}

func TestMigrateAppLayoutDevelopmentAndLinux(t *testing.T) {
	root := t.TempDir()
	d := appDirs{Config: filepath.Join(root, "config"), Data: root, Cache: filepath.Join(root, "cache"), Logs: filepath.Join(root, "logs"), Development: true}
	if err := os.WriteFile(filepath.Join(root, "search-engine.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errs := migrateAppLayout(d); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "search-engine.json")); err != nil {
		t.Fatal("engine choice not moved to config")
	}
}

func TestSeedDevelopmentConfig(t *testing.T) {
	root := t.TempDir()
	installed := appDirs{Config: filepath.Join(root, "installed")}
	dev := appDirs{Config: filepath.Join(root, "dev", "config")}
	if err := os.MkdirAll(installed.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed.Config, "preferences.json"), []byte(`{"language":"ja"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	seedDevelopmentConfig(dev, installed)
	b, err := os.ReadFile(filepath.Join(dev.Config, "preferences.json"))
	if err != nil || string(b) != `{"language":"ja"}` {
		t.Fatalf("seeded %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(installed.Config, "preferences.json")); err != nil {
		t.Fatal("installed preferences must stay")
	}
}
