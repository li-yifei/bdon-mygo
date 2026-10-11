package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The normal application's process environment stays intact. Isolation is
// applied to each ADB child only: AOSP loads its user key from HOME/.android.
type adbIsolation struct{ root, port string }

var isolatedADB *adbIsolation

func configureADBIsolation() error {
	root, port := os.Getenv("OURNOTES_PAIRING_TEST_ROOT"), os.Getenv("OURNOTES_PAIRING_TEST_PORT")
	if root == "" && port == "" {
		return nil
	}
	n, err := strconv.Atoi(port)
	if !filepath.IsAbs(root) || err != nil || n < 1024 || n > 65535 || n == 5037 {
		return errors.New("invalid pairing test isolation settings")
	}
	root = filepath.Clean(root)
	for _, dir := range []string{root, filepath.Join(root, "home"), filepath.Join(root, "home", ".android"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	// Refuse to target a foreign server on first use of this fresh session.
	if _, err := os.Stat(filepath.Join(root, "adb-path")); os.IsNotExist(err) {
		conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
		if err == nil {
			conn.Close()
			return errors.New("pairing test port is in use; restart dev:pairing")
		}
	}
	isolatedADB = &adbIsolation{root: root, port: port}
	return nil
}
func (s *adbIsolation) environment(parent []string) []string {
	blocked := map[string]bool{"HOME": true, "ADB_VENDOR_KEYS": true, "ADB_SERVER_SOCKET": true, "ANDROID_ADB_SERVER_ADDRESS": true, "ANDROID_ADB_SERVER_PORT": true, "ANDROID_SERIAL": true, "ANDROID_USER_HOME": true, "ANDROID_SDK_HOME": true, "ADB_USB": true, "ADB_MDNS_AUTO_CONNECT": true, "ANDROID_ADB_LOG_PATH": true, "TMPDIR": true, "USERPROFILE": true, "TEMP": true, "TMP": true}
	env := make([]string, 0, len(parent)+12)
	for _, entry := range parent {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			env = append(env, entry)
		}
	}
	return append(env,
		"HOME="+filepath.Join(s.root, "home"),
		"USERPROFILE="+filepath.Join(s.root, "home"),
		"ANDROID_USER_HOME="+filepath.Join(s.root, "home", ".android"),
		"ADB_VENDOR_KEYS="+filepath.Join(s.root, "home", ".android"),
		"ADB_SERVER_SOCKET=tcp:localhost:"+s.port,
		"ADB_USB=0", "ADB_MDNS_AUTO_CONNECT=0",
		"ANDROID_ADB_LOG_PATH="+filepath.Join(s.root, "adb.log"),
		"TMPDIR="+filepath.Join(s.root, "tmp"),
		"TEMP="+filepath.Join(s.root, "tmp"),
		"TMP="+filepath.Join(s.root, "tmp"))
}
func adbCommand(ctx context.Context, bin string, args ...string) *exec.Cmd {
	if isolatedADB != nil {
		args = append([]string{"-P", isolatedADB.port}, args...)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	configureChildProcess(cmd)
	// A command that starts the adb server (devices, start-server, pair, ...)
	// leaves the daemon holding the inherited stdout/stderr pipes on Windows, so
	// Output/CombinedOutput would wait for the server to exit. Stop waiting for
	// the pipes shortly after adb itself has exited.
	cmd.WaitDelay = 3 * time.Second
	if isolatedADB != nil {
		cmd.Env = isolatedADB.environment(os.Environ())
		// Dev launcher reads this exact binary path to stop only its private server.
		_ = os.WriteFile(filepath.Join(isolatedADB.root, "adb-path"), []byte(bin), 0600)
	}
	return cmd
}
