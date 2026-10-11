package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The app's storage is split by kind into config, data, cache and logs
// directories (see app_paths.go). The data directory holds the pulled saves,
// imported engines and other local files; the resource service starts there. Nothing
// is written into the app bundle, so a release build carries no personal data
// and keeps a valid code signature.

// isDevelopment reports a run of `mygo dev`.
func isDevelopment() bool { return os.Getenv("MYGO_ENV") == "development" }

// defaultResourceDir is the data directory.
func defaultResourceDir() string { return currentAppDirs().Data }

// legacyStorageDirs lists where earlier releases kept their storage, most
// specific first: inside the running bundle, inside a previous bundle next to
// it or in the Applications folders, and next to a Linux executable.
func legacyStorageDirs(executable, home string) []string {
	var dirs []string
	add := func(p string) {
		p = filepath.Clean(p)
		for _, d := range dirs {
			if d == p {
				return
			}
		}
		dirs = append(dirs, p)
	}
	legacyBundle := product.LegacyName + ".app"
	inBundle := func(app string) string { return filepath.Join(app, "Contents", "Resources", product.LegacyStorageDir) }
	dir := filepath.Dir(executable)
	if filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		app := filepath.Dir(filepath.Dir(dir))
		add(inBundle(app))
		add(inBundle(filepath.Join(filepath.Dir(app), legacyBundle)))
		add(inBundle(filepath.Join("/Applications", legacyBundle)))
		if home != "" {
			add(inBundle(filepath.Join(home, "Applications", legacyBundle)))
		}
		return dirs
	}
	if executable != "" {
		add(filepath.Join(dir, product.DevStorageDir))
	}
	return dirs
}

// dirIsEmpty reports whether dir is missing or holds only Finder metadata.
func dirIsEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() != ".DS_Store" {
			return false, nil
		}
	}
	return true, nil
}

// migrateLegacyStorage copies the first non-empty legacy storage directory to
// dest when dest is missing or empty. It returns the source used, or "" when
// nothing was migrated. The copy is staged next to dest and renamed into
// place, so an interrupted copy is retried on the next launch. Legacy data is
// left untouched.
func migrateLegacyStorage(dest string, candidates []string) (string, error) {
	empty, err := dirIsEmpty(dest)
	if err != nil || !empty {
		return "", err
	}
	for _, src := range candidates {
		if filepath.Clean(src) == filepath.Clean(dest) {
			continue
		}
		if info, err := os.Stat(src); err != nil || !info.IsDir() {
			continue
		}
		if srcEmpty, err := dirIsEmpty(src); err != nil || srcEmpty {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return "", err
		}
		stage, err := os.MkdirTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".migrating-")
		if err != nil {
			return "", err
		}
		if err := copyTree(src, stage); err != nil {
			os.RemoveAll(stage)
			return "", fmt.Errorf("migrating legacy storage %s: %w", src, err)
		}
		if err := os.Chmod(stage, 0o700); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
		// dest is missing or empty here; clear it so the rename can land.
		os.Remove(filepath.Join(dest, ".DS_Store"))
		if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
			os.RemoveAll(stage)
			return "", err
		}
		if err := os.Rename(stage, dest); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
		return src, nil
	}
	return "", nil
}

// copyTree copies regular files and directories, keeping permission bits.
// Symlinks and special files are skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".event-pull-") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		}
		return nil
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
