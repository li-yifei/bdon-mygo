package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// appDirs are the app's per-user directories, split by kind, following each
// platform's conventions:
//
//	kind    macOS                                   Windows                          Linux
//	config  ~/Library/Application Support/<dir>     %APPDATA%\<dir>                  $XDG_CONFIG_HOME/<dir> (~/.config)
//	data    ~/Library/Application Support/<dir>     %LOCALAPPDATA%\<dir>\Data        $XDG_DATA_HOME/<dir> (~/.local/share)
//	cache   ~/Library/Caches/<dir>                  %LOCALAPPDATA%\<dir>\Cache       $XDG_CACHE_HOME/<dir> (~/.cache)
//	logs    ~/Library/Logs/<dir>                    %LOCALAPPDATA%\<dir>\Logs        $XDG_STATE_HOME/<dir> (~/.local/state)
//
// config holds small settings files (preferences.json, adb-transport.json,
// search-engine.json, save.env); data the user's data (pulled saves, imported
// engines); cache what can be made again (verified datasets,
// downloaded Platform-Tools, temporary stages); logs the log file.
//
// On macOS and Linux the cache and logs match mygo.App.Path(PathCache) and
// PathLogs; Windows splits %LOCALAPPDATA% further, so the paths are computed
// here, from an injectable environment, for every platform alike.
//
// Portable mode (Windows and Linux archives): a directory named "data" or a
// file named "portable" next to the executable keeps everything in
// <exe dir>/data/{config,data,cache,logs}.
//
// Development (MYGO_ENV=development) keeps one directory, OURNOTES_DEV_STORAGE
// or .local-storage in the working directory: data in it as before, and
// config/, cache/ and logs/ inside it.
type appDirs struct {
	Config, Data, Cache, Logs string
	Portable, Development     bool
}

// pathEnv is what resolveAppDirs reads from the system; tests inject it.
type pathEnv struct {
	goos   string
	home   string
	exeDir string
	cwd    string
	getenv func(string) string
	isDir  func(string) bool
	isFile func(string) bool
}

func systemPathEnv() pathEnv {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	env := pathEnv{goos: runtime.GOOS, home: home, cwd: cwd, getenv: os.Getenv, isDir: isDir, isFile: isFile}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		env.exeDir = filepath.Dir(exe)
	}
	return env
}

// currentAppDirs resolves the directories from the process environment. It is
// cheap, and recomputed on each call so tests can change the environment.
func currentAppDirs() appDirs { return resolveAppDirs(systemPathEnv()) }

func resolveAppDirs(env pathEnv) appDirs {
	name := product.ConfigDir
	absEnv := func(key, fallback string) string {
		if v := env.getenv(key); v != "" && filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
		return fallback
	}
	split := func(base string) appDirs {
		return appDirs{Config: filepath.Join(base, "config"), Data: filepath.Join(base, "data"), Cache: filepath.Join(base, "cache"), Logs: filepath.Join(base, "logs")}
	}
	if env.getenv("MYGO_ENV") == "development" {
		base := absEnv("OURNOTES_DEV_STORAGE", filepath.Join(env.cwd, product.DevStorageDir))
		d := split(base)
		d.Data, d.Development = base, true
		return d
	}
	if env.goos != "darwin" && env.exeDir != "" {
		if env.isDir(filepath.Join(env.exeDir, "data")) || env.isFile(filepath.Join(env.exeDir, "portable")) {
			d := split(filepath.Join(env.exeDir, "data"))
			d.Portable = true
			return d
		}
	}
	switch env.goos {
	case "darwin":
		support := filepath.Join(env.home, "Library", "Application Support", name)
		return appDirs{Config: support, Data: support, Cache: filepath.Join(env.home, "Library", "Caches", name), Logs: filepath.Join(env.home, "Library", "Logs", name)}
	case "windows":
		roaming := absEnv("APPDATA", filepath.Join(env.home, "AppData", "Roaming"))
		local := filepath.Join(absEnv("LOCALAPPDATA", filepath.Join(env.home, "AppData", "Local")), name)
		return appDirs{Config: filepath.Join(roaming, name), Data: filepath.Join(local, "Data"), Cache: filepath.Join(local, "Cache"), Logs: filepath.Join(local, "Logs")}
	}
	return appDirs{
		Config: filepath.Join(absEnv("XDG_CONFIG_HOME", filepath.Join(env.home, ".config")), name),
		Data:   filepath.Join(absEnv("XDG_DATA_HOME", filepath.Join(env.home, ".local", "share")), name),
		Cache:  filepath.Join(absEnv("XDG_CACHE_HOME", filepath.Join(env.home, ".cache")), name),
		Logs:   filepath.Join(absEnv("XDG_STATE_HOME", filepath.Join(env.home, ".local", "state")), name),
	}
}

// productConfigPath joins elem under the config directory.
func productConfigPath(elem ...string) (string, error) {
	return filepath.Join(append([]string{currentAppDirs().Config}, elem...)...), nil
}

// productCachePath joins elem under the cache directory.
func productCachePath(elem ...string) string {
	return filepath.Join(append([]string{currentAppDirs().Cache}, elem...)...)
}

// cacheStage makes a temporary directory in the cache for work that is
// discarded afterwards, such as a pull before it is processed.
func cacheStage(prefix string) (string, error) {
	dir := currentAppDirs().Cache
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, prefix)
}

// appLayoutMove is one rename of migrateAppLayout.
type appLayoutMove struct{ from, to string }

// appLayoutMoves lists what earlier layouts kept elsewhere: everything lived
// in one directory (the config directory on macOS and Windows, the data
// directory on Linux, .local-storage in development), with Platform-Tools
// under the config directory. Re-creatable files go to the cache, the engine
// choice to the config. User data stays where it is.
func appLayoutMoves(d appDirs) []appLayoutMove {
	var moves []appLayoutMove
	add := func(from, to string) {
		if filepath.Clean(from) != filepath.Clean(to) {
			moves = append(moves, appLayoutMove{from, to})
		}
	}
	add(filepath.Join(d.Data, "datasets"), filepath.Join(d.Cache, "datasets"))
	add(filepath.Join(d.Config, "tools"), filepath.Join(d.Cache, "tools"))
	add(filepath.Join(d.Data, "search-engine.json"), filepath.Join(d.Config, "search-engine.json"))
	if entries, err := os.ReadDir(d.Data); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), ".event-pull-") {
				add(filepath.Join(d.Data, e.Name()), filepath.Join(d.Cache, e.Name()))
			}
		}
	}
	return moves
}

// migrateAppLayout moves files of earlier layouts into place by renaming,
// only when the destination does not exist yet. Nothing is deleted: a move
// that fails (another volume, a file in use) leaves the file where it was,
// and the cache is filled again when needed. It returns the moves done.
func migrateAppLayout(d appDirs) ([]appLayoutMove, []error) {
	var done []appLayoutMove
	var errs []error
	for _, m := range appLayoutMoves(d) {
		if _, err := os.Lstat(m.from); err != nil {
			continue
		}
		if _, err := os.Lstat(m.to); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(m.to), 0o700); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(m.from, m.to); err != nil {
			errs = append(errs, fmt.Errorf("moving %s to %s: %w", m.from, m.to, err))
			continue
		}
		done = append(done, m)
	}
	return done, errs
}

// seedDevelopmentConfig copies the settings files of the installed app into
// a development config directory that has none, so a development run starts
// with the same preferences. The installed app's files are left as they are.
func seedDevelopmentConfig(dev, installed appDirs) {
	for _, name := range []string{"preferences.json", "adb-transport.json"} {
		dst := filepath.Join(dev.Config, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		src := filepath.Join(installed.Config, name)
		info, err := os.Stat(src)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.MkdirAll(dev.Config, 0o700); err != nil {
			return
		}
		_ = copyFile(src, dst, 0o600)
	}
}

// prepareAppDirs migrates earlier layouts and creates the directories.
func prepareAppDirs(d appDirs) {
	if d.Development {
		installed := systemPathEnv()
		installed.getenv = func(key string) string {
			if key == "MYGO_ENV" {
				return ""
			}
			return os.Getenv(key)
		}
		seedDevelopmentConfig(d, resolveAppDirs(installed))
	}
	moves, errs := migrateAppLayout(d)
	for _, m := range moves {
		log.Printf("moved %s to %s", m.from, m.to)
	}
	for _, err := range errs {
		log.Printf("storage layout: %v", err)
	}
	for _, dir := range []string{d.Config, d.Data, d.Cache, d.Logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("creating %s: %v", dir, err)
		}
	}
}

// logFileLimit is the size at which the log file is rotated to .1.
const logFileLimit = 2 << 20

// openLogFile adds <logs>/<name>.log to the standard logger, which keeps
// writing to stderr too. A file over logFileLimit is first rotated to .1.
func openLogFile(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, product.ConfigDir+".log")
	if info, err := os.Stat(path); err == nil && info.Size() > logFileLimit {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	log.SetOutput(logWriter{f})
	return f, nil
}

// logWriter writes to the log file and to stderr, which a Windows GUI
// program may not have: stderr errors are ignored.
type logWriter struct{ file io.Writer }

func (w logWriter) Write(p []byte) (int, error) {
	_, _ = os.Stderr.Write(p)
	return w.file.Write(p)
}
