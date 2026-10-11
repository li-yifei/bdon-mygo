package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testADBZip(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	_, err := z.Create("platform-tools/")
	if err != nil {
		t.Fatal(err)
	}
	f, err := z.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("test executable"))
	z.Close()
	return buf.Bytes()
}
func TestPlatformToolsInstall(t *testing.T) {
	data := testADBZip(t, "platform-tools/adb.exe")
	checksum := fmt.Sprintf("%x", sha1.Sum(data))
	for _, test := range []struct {
		name, checksum string
		success        bool
	}{{"valid", checksum, true}, {"corrupt", strings.Repeat("0", 40), false}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/repository2-1.xml" {
					fmt.Fprintf(w, `<sdk-repository><remotePackage path="platform-tools"><archives><archive><host-os>windows</host-os><complete><size>%d</size><checksum>%s</checksum><url>platform-tools_test-win.zip</url></complete></archive></archives></remotePackage></sdk-repository>`, len(data), test.checksum)
					return
				}
				w.Write(data)
			}))
			defer server.Close()
			dir := t.TempDir()
			path, err := installPlatformTools(context.Background(), server.Client(), server.URL+"/", dir, "windows")
			if test.success {
				if err != nil {
					t.Fatal(err)
				}
				if b, e := os.ReadFile(path); e != nil || string(b) != "test executable" {
					t.Fatalf("bad installed file %s %v", b, e)
				}
			} else {
				if err == nil {
					t.Fatal("accepted checksum mismatch")
				}
				if _, e := os.Stat(filepath.Join(dir, "platform-tools")); !os.IsNotExist(e) {
					t.Fatal("published rejected download")
				}
			}
		})
	}
}
func TestPlatformToolsRejectZipTraversal(t *testing.T) {
	for _, name := range []string{"../outside", "platform-tools/../../outside", "/platform-tools/adb", "platform-tools\\..\\..\\outside", "platform-tools/C:evil"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "archive.zip")
			os.WriteFile(path, testADBZip(t, name), 0600)
			if err := extractPlatformTools(path, filepath.Join(dir, "extract")); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestPlatformToolsRejectMetadataURL(t *testing.T) {
	xml := `<sdk-repository><remotePackage path="platform-tools"><archives><archive><host-os>macosx</host-os><complete><size>1</size><checksum>0000000000000000000000000000000000000000</checksum><url>../../x.zip</url></complete></archive></archives></remotePackage></sdk-repository>`
	if _, err := platformToolsMetadata(strings.NewReader(xml), "darwin"); err == nil {
		t.Fatal("unsafe metadata URL accepted")
	}
}
func TestPlatformToolsLiveDownload(t *testing.T) {
	if os.Getenv("OURNOTES_TEST_ADB_DOWNLOAD") != "1" {
		t.Skip("opt-in official network download")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path, err := installPlatformTools(ctx, http.DefaultClient, androidRepositoryURL, t.TempDir(), "darwin")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Verified official Google archive and adb version:", path)
}

func TestPlatformToolsDownloadWaitsForTheLicenseAgreement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("PATH", "")
	t.Setenv("OURNOTES_ADB", "")
	t.Setenv("OURNOTES_DEV_STORAGE", "")
	t.Setenv("MYGO_ENV", "")
	previous := platformToolsTermsAccepted
	t.Cleanup(func() { platformToolsTermsAccepted = previous })
	platformToolsTermsAccepted = func() bool { return false }
	if _, err := resolveADB(); !errors.Is(err, errPlatformToolsTermsRequired) {
		t.Fatalf("want the license agreement first, got %v", err)
	}
}
