package main

import (
	"bytes"
	"encoding/hex"
	"github.com/li-yifei/bdon-mygo/internal/i18n"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestExtractLocalPlayerExactInteger(t *testing.T) {
	raw := `{"_player": {"_profileId":9223372036854775807,"_name":"test"}}`
	player, id, e := extractLocalPlayer([]byte(raw))
	if e != nil || id != "9223372036854775807" || player != `{"_profileId":9223372036854775807,"_name":"test"}` {
		t.Fatalf("integer preservation failed: %v", e)
	}
}
func TestPlayerSaveRejectsStaleDecoded(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "android", "adb")
	os.MkdirAll(dir, 0700)
	acc := filepath.Join(root, "android", "account")
	os.MkdirAll(acc, 0700)
	p := filepath.Join(acc, "player.json")
	os.WriteFile(p, []byte(`{"_profileId":"123"}`), 0600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	os.WriteFile(filepath.Join(dir, "raw"), make([]byte, 96), 0600)
	if _, _, _, e := readLocalPlayerSave(root); e == nil {
		t.Fatal("accepted stale decoded player")
	}
}
func TestPlayerSaveFallbackServer(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "android", "account")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "player.json"), []byte(`{"_profileId":"123"}`), 0600)
	os.WriteFile(filepath.Join(dir, "player.json.meta.json"), []byte(`{"package":"com.bilibili.sirius"}`), 0600)
	_, id, server, e := readLocalPlayerSave(root)
	if e != nil || id != "123" || server != "intl" {
		t.Fatalf("fallback: %v %s", e, server)
	}
}
func TestLocalPlayerDeviceCache(t *testing.T) {
	if os.Getenv("OURNOTES_SAVE_TEST_ROOT") == "" {
		t.Skip("set OURNOTES_SAVE_TEST_ROOT to verify device cache")
	}
	text, id, server, e := readLocalPlayerSave(os.Getenv("OURNOTES_SAVE_TEST_ROOT"))
	if e != nil || len(text) < 1000 || id == "" || server != "jp" {
		t.Fatalf("device cache: %v", e)
	}
	t.Logf("decrypted player bytes=%d server=%s", len(text), server)
}

func TestSaveCredentialsPathFallback(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".config", product.CredentialsDir, "save.env")
	legacy := filepath.Join(home, ".config", product.LegacyCredentialsDir, "save.env")
	if got := saveCredentialsPath("darwin", home, filepath.Join(home, "Library", "Application Support", product.ConfigDir)); got != current {
		t.Fatalf("no file: %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := saveCredentialsPath("darwin", home, filepath.Join(home, "Library", "Application Support", product.ConfigDir)); got != legacy {
		t.Fatalf("legacy only: %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(current), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := saveCredentialsPath("darwin", home, filepath.Join(home, "Library", "Application Support", product.ConfigDir)); got != current {
		t.Fatalf("both: %q", got)
	}
}

func TestSaveCredentialsPathPerPlatform(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".config", product.LegacyCredentialsDir, "save.env")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	appData := filepath.Join(home, "AppData", "Roaming", product.ConfigDir)
	if got, want := saveCredentialsPath("windows", home, appData), filepath.Join(appData, "save.env"); got != want {
		t.Fatalf("windows: %q, want %q", got, want)
	}
	xdg := filepath.Join(home, "xdg", product.ConfigDir)
	if got, want := saveCredentialsPath("linux", home, xdg), filepath.Join(xdg, "save.env"); got != want {
		t.Fatalf("linux: %q, want %q", got, want)
	}
	if got, want := saveCredentialsPath("linux", home, ""), filepath.Join(home, ".config", product.CredentialsDir, "save.env"); got != want {
		t.Fatalf("linux without config dir: %q, want %q", got, want)
	}
}

func TestPulledEncryptedSaveWithoutCredentialsNamesTheCredentialsFile(t *testing.T) {
	root := t.TempDir()
	adbDir := filepath.Join(root, "android", "adb")
	if err := os.MkdirAll(adbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adbDir, "encrypted"), bytes.Repeat([]byte{0xA5}, 128), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "save.env")
	previous := extensionSaveCredentials
	extensionSaveCredentials = nil
	t.Cleanup(func() { extensionSaveCredentials = previous })
	t.Setenv("OURNOTES_SAVE_CREDENTIALS", missing)
	t.Setenv("OURNOTES_SAVE_KEY", "")
	t.Setenv("OURNOTES_SAVE_MAGIC", "")
	_, _, _, err := readLocalPlayerSave(root)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("want an error naming %s, got %v", missing, err)
	}
	if _, _, _, err = readLocalPlayerSave(t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), i18n.T("save.error.notPulled")) {
		t.Fatalf("an empty store should ask for a pull, got %v", err)
	}
}

func TestBuiltInSaveCredentialsApplyWhenNoneAreConfigured(t *testing.T) {
	t.Setenv("OURNOTES_SAVE_CREDENTIALS", filepath.Join(t.TempDir(), "absent.env"))
	t.Setenv("OURNOTES_SAVE_KEY", "")
	t.Setenv("OURNOTES_SAVE_MAGIC", "")
	previous := extensionSaveCredentials
	t.Cleanup(func() { extensionSaveCredentials = previous })
	want := [32]byte{1, 2, 3}
	extensionSaveCredentials = func() (key, magic [32]byte, ok bool) { return want, want, true }
	key, magic, err := localSaveCredentials()
	if err != nil || key != want || magic != want {
		t.Fatalf("built-in credentials not used: %v", err)
	}
}

func TestEmbeddedCredentialsRoundTripThroughTheGenerator(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "zz_embedded_save_credentials.go")
	key := strings.Repeat("0123456789abcdef", 4)
	magic := strings.Repeat("fedcba9876543210", 4)
	cmd := exec.Command("python3", "scripts/embed-save-credentials.py", out)
	cmd.Env = append(os.Environ(), "OURNOTES_SAVE_KEY="+key, "OURNOTES_SAVE_MAGIC="+magic)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("python3 unavailable: %v %s", err, b)
	}
	src, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), key) || strings.Contains(string(src), magic) {
		t.Fatal("generated file must not contain the plain credentials")
	}
	parts := regexp.MustCompile(`maskedSecret\("([0-9a-f]{64})"\)`).FindAllStringSubmatch(string(src), -1)
	if len(parts) != 2 {
		t.Fatalf("unexpected generated file:\n%s", src)
	}
	for i, want := range []string{key, magic} {
		got := maskedSecret(parts[i][1])
		if hex.EncodeToString(got[:]) != want {
			t.Fatalf("credential %d does not round-trip", i)
		}
	}
}
