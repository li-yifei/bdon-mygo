package main

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

const androidRepositoryURL = "https://dl.google.com/android/repository/"

var adbInstallMu sync.Mutex

type platformToolsArchive struct {
	HostOS   string `xml:"host-os"`
	Complete struct {
		Size     int64  `xml:"size"`
		Checksum string `xml:"checksum"`
		URL      string `xml:"url"`
	} `xml:"complete"`
}
type platformToolsRepository struct {
	Packages []struct {
		Path     string                 `xml:"path,attr"`
		Archives []platformToolsArchive `xml:"archives>archive"`
	} `xml:"remotePackage"`
}

func platformToolsHost(goos string) (string, error) {
	switch goos {
	case "darwin":
		return "macosx", nil
	case "linux", "windows":
		return goos, nil
	}
	return "", i18n.New("adb.error.unsupportedOS", "os", goos)
}
func platformToolsMetadata(r io.Reader, goos string) (platformToolsArchive, error) {
	host, err := platformToolsHost(goos)
	if err != nil {
		return platformToolsArchive{}, err
	}
	var repo platformToolsRepository
	if err = xml.NewDecoder(io.LimitReader(r, 8<<20)).Decode(&repo); err != nil {
		return platformToolsArchive{}, err
	}
	for _, p := range repo.Packages {
		if p.Path != "platform-tools" {
			continue
		}
		for _, a := range p.Archives {
			if a.HostOS != host {
				continue
			}
			if a.Complete.Size <= 0 || a.Complete.Size > 128<<20 || len(a.Complete.Checksum) != 40 || filepath.Base(a.Complete.URL) != a.Complete.URL || !strings.HasPrefix(a.Complete.URL, "platform-tools_") || !strings.HasSuffix(a.Complete.URL, ".zip") {
				return a, i18n.New("adb.error.badMetadata")
			}
			if _, err := hex.DecodeString(a.Complete.Checksum); err != nil {
				return a, err
			}
			return a, nil
		}
	}
	return platformToolsArchive{}, i18n.New("adb.error.noPlatformTools", "host", host)
}
func adbCachedPath() (string, error) {
	name := "adb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// Platform-Tools can be downloaded again: they live in the cache.
	return productCachePath("tools", "platform-tools", name), nil
}

// errPlatformToolsTermsRequired reports that downloading Platform-Tools waits
// for the user's agreement to the Android SDK License Agreement.
var errPlatformToolsTermsRequired = errors.New("android sdk license agreement not accepted")

// platformToolsTermsAccepted is a variable so tests can stub the preference.
var platformToolsTermsAccepted = func() bool { return loadPreferences().PlatformToolsTermsAccepted }

// acceptPlatformToolsTerms persists the user's agreement.
func acceptPlatformToolsTerms() error {
	return updatePreferences(func(p *appPreferences) { p.PlatformToolsTermsAccepted = true })
}

// resolveADB prefers configured/system SDK tools and installs an official copy
// into app-owned storage when none are available. It never changes PATH.
func resolveADB() (string, error) {
	if bin := adbBinary(); bin != "" {
		return bin, nil
	}
	adbInstallMu.Lock()
	defer adbInstallMu.Unlock()
	bin, err := adbCachedPath()
	if err != nil {
		return "", err
	}
	if st, e := os.Stat(bin); e == nil && !st.IsDir() {
		return bin, nil
	}
	// Google's Platform-Tools may not be redistributed; the app downloads them
	// only after the user agrees to the Android SDK License Agreement.
	if !platformToolsTermsAccepted() {
		return "", i18n.Wrap(errPlatformToolsTermsRequired, "adb.error.termsRequired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return installPlatformTools(ctx, http.DefaultClient, androidRepositoryURL, filepath.Dir(filepath.Dir(bin)), runtime.GOOS)
}
func fetchADBFile(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, i18n.Wrap(fmt.Errorf("HTTP %d", resp.StatusCode), "adb.error.download")
	}
	return resp, nil
}
func installPlatformTools(ctx context.Context, client *http.Client, base, toolsDir, goos string) (string, error) {
	resp, err := fetchADBFile(ctx, client, base+"repository2-1.xml")
	if err != nil {
		return "", i18n.Wrap(err, "adb.error.repository")
	}
	archive, err := platformToolsMetadata(resp.Body, goos)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(toolsDir, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(toolsDir, ".platform-tools-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	zipPath := filepath.Join(stage, "download.zip")
	resp, err = fetchADBFile(ctx, client, base+archive.Complete.URL)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(zipPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		resp.Body.Close()
		return "", err
	}
	h := sha1.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, archive.Complete.Size+1))
	closeErr := f.Close()
	resp.Body.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n != archive.Complete.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), archive.Complete.Checksum) {
		return "", i18n.New("adb.error.checksum")
	}
	if err = extractPlatformTools(zipPath, stage); err != nil {
		return "", err
	}
	name := "adb"
	if goos == "windows" {
		name += ".exe"
	}
	stagedBin := filepath.Join(stage, "platform-tools", name)
	st, err := os.Stat(stagedBin)
	if err != nil || st.IsDir() {
		return "", i18n.New("adb.error.missingBinary")
	}
	if goos != "windows" {
		if err = os.Chmod(stagedBin, 0755); err != nil {
			return "", err
		}
	}
	if goos == runtime.GOOS {
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		probe := exec.CommandContext(probeCtx, stagedBin, "version")
		configureChildProcess(probe)
		if out, e := probe.CombinedOutput(); e != nil {
			return "", i18n.Wrap(fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), e), "adb.error.cannotRun")
		}
	}
	destination := filepath.Join(toolsDir, "platform-tools")
	if err = os.Rename(filepath.Join(stage, "platform-tools"), destination); err != nil {
		return "", err
	}
	return filepath.Join(destination, name), nil
}
func extractPlatformTools(zipPath, destination string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	var total uint64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		clean := filepath.ToSlash(filepath.Clean(name))
		if (clean != "platform-tools" && !strings.HasPrefix(clean, "platform-tools/")) || strings.HasPrefix(name, "/") || strings.Contains(name, ":") || f.Mode()&os.ModeSymlink != 0 {
			return i18n.New("adb.error.unsafeZip", "name", f.Name)
		}
		total += f.UncompressedSize64
		if total > 512<<20 {
			return i18n.New("adb.error.zipTooLarge")
		}
		path := filepath.Join(destination, filepath.FromSlash(clean))
		if f.FileInfo().IsDir() {
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		input, e := f.Open()
		if e != nil {
			return e
		}
		output, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			input.Close()
			return e
		}
		_, copyErr := io.Copy(output, io.LimitReader(input, 512<<20))
		closeErr := output.Close()
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
