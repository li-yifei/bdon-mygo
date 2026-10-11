package main

import (
	"context"
	"crypto/sha256"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestADBIsolationCommandBoundary(t *testing.T) {
	root := t.TempDir()
	s := &adbIsolation{root: root, port: "5038"}
	parent := []string{"HOME=/real-user", "ADB_VENDOR_KEYS=/real-key", "ADB_SERVER_SOCKET=tcp:localhost:5037", "ANDROID_SERIAL=usb-device", "ADB_USB=1", "PATH=/bin"}
	env := s.environment(parent)
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, want := range []string{"HOME=" + filepath.Join(root, "home"), "ADB_VENDOR_KEYS=" + filepath.Join(root, "home", ".android"), "ADB_SERVER_SOCKET=tcp:localhost:5038", "ADB_USB=0", "ADB_MDNS_AUTO_CONNECT=0", "PATH=/bin"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Fatalf("missing isolated setting %q", want)
		}
	}
	for _, forbidden := range []string{"/real-user", "/real-key", "usb-device", "localhost:5037"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("inherited forbidden value %q", forbidden)
		}
	}
	original := isolatedADB
	defer func() { isolatedADB = original }()
	isolatedADB = s
	cmd := adbCommand(context.Background(), "/adb", "pair", "127.0.0.1:1234", "one-time-code")
	if got := strings.Join(cmd.Args, " "); got != "/adb -P 5038 pair 127.0.0.1:1234 one-time-code" {
		t.Fatalf("wrong argv: %s", got)
	}
	if adbCredentialsPath() != filepath.Join(root, "home", ".android", "adbkey") {
		t.Fatal("system credentials selected")
	}
	if validADBTransport("direct-usb", "") == nil {
		t.Fatal("direct mode escaped isolation")
	}
	if _, err := (&adbService{}).ReleaseSystemDevice("existing-device"); err == nil {
		t.Fatal("system USB detach accepted")
	}
}
func TestADBIsolationRejectsSystemPort(t *testing.T) {
	t.Setenv("OURNOTES_PAIRING_TEST_ROOT", t.TempDir())
	t.Setenv("OURNOTES_PAIRING_TEST_PORT", "5037")
	if configureADBIsolation() == nil {
		t.Fatal("accepted system server port")
	}
}
func TestADBIsolationLive(t *testing.T) {
	if os.Getenv("OURNOTES_TEST_ISOLATED_ADB") != "1" {
		t.Skip("opt-in private server test")
	}
	old := isolatedADB
	defer func() { isolatedADB = old }()
	bin, err := resolveADB()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	systemKey := filepath.Join(home, ".android", "adbkey")
	before, keyErr := os.ReadFile(systemKey)
	systemPID := func() string {
		b, _ := exec.Command("lsof", "-n", "-P", "-iTCP:5037", "-sTCP:LISTEN", "-t").Output()
		return string(b)
	}
	pid := systemPID()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	t.Setenv("OURNOTES_PAIRING_TEST_ROOT", t.TempDir())
	t.Setenv("OURNOTES_PAIRING_TEST_PORT", port)
	if err = configureADBIsolation(); err != nil {
		t.Fatal(err)
	}
	defer adbCommand(context.Background(), bin, "kill-server").Run()
	out, err := adbCommand(context.Background(), bin, "start-server").CombinedOutput()
	if err != nil {
		t.Fatalf("private server start: %v %s", err, out)
	}
	devices, err := adbRun("", "devices", "-l")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(devices)) != "List of devices attached" {
		t.Fatalf("fresh server inherited devices: %s", devices)
	}
	if info, err := os.Stat(adbCredentialsPath()); err != nil || info.Size() == 0 {
		t.Fatal("fresh key not generated")
	}
	after, afterErr := os.ReadFile(systemKey)
	if (keyErr == nil) != (afterErr == nil) || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("system key changed")
	}
	if systemPID() != pid {
		t.Fatal("system server PID changed")
	}
	t.Log("private server has zero devices and a new key; system key and server PID preserved")
}

func TestADBCommandDoesNotWaitForServerPipes(t *testing.T) {
	if cmd := adbCommand(context.Background(), "adb", "devices"); cmd.WaitDelay <= 0 {
		t.Fatal("adb commands must bound the wait for pipes inherited by the adb server")
	}
}
