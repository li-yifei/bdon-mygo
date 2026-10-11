package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pgaskin/go-adb/adb/adbproto/atransport"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

const adbMaxFile int64 = 16 << 20

// adbDevice is intentionally small so it can be rendered directly by the WebView.
type adbDevice struct {
	Serial  string `json:"serial"`
	State   string `json:"state"`
	Model   string `json:"model,omitempty"`
	Product string `json:"product,omitempty"`
}

type adbPackage struct {
	Package  string `json:"package"`
	Root     string `json:"root"`
	Variant  string `json:"variant"`
	HasFiles bool   `json:"hasFiles"`
}

type adbAuthResult struct {
	Mode            string `json:"mode"`
	CredentialsPath string `json:"credentialsPath,omitempty"`
	Serial          string `json:"serial,omitempty"`
	Output          string `json:"output"`
	Message         string `json:"message"`
}

type adbPullResult struct {
	Output     string   `json:"output"`
	Package    string   `json:"package"`
	Serial     string   `json:"serial"`
	Root       string   `json:"root"`
	Files      int      `json:"files"`
	Bytes      int64    `json:"bytes"`
	Skipped    int      `json:"skipped"`
	Errors     []string `json:"errors,omitempty"`
	StartedAt  string   `json:"startedAt"`
	FinishedAt string   `json:"finishedAt"`
	Message    string   `json:"message"`
}

type adbService struct {
	resources    *resourceService
	transportMu  sync.Mutex
	mode         string
	address      string
	direct       *atransport.Transport
	directSerial string
}

func adbBinary() string {
	if p := os.Getenv("OURNOTES_ADB"); p != "" {
		return p
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p
	}
	// Android SDK installs on macOS commonly use this location when PATH is not inherited by a GUI app.
	if home, err := os.UserHomeDir(); err == nil {
		for _, p := range []string{filepath.Join(home, "Library/Android/sdk/platform-tools/adb"), filepath.Join(home, "Android/Sdk/platform-tools/adb")} {
			if info, e := os.Stat(p); e == nil && !info.IsDir() {
				return p
			}
		}
	}
	return ""
}

func adbRun(serial string, args ...string) ([]byte, error) {
	bin, resolveErr := resolveADB()
	if resolveErr != nil {
		return nil, resolveErr
	}
	all := make([]string, 0, len(args)+2)
	if serial != "" {
		all = append(all, "-s", serial)
	}
	all = append(all, args...)
	cmd := adbCommand(context.Background(), bin, all...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("adb %s: %s", strings.Join(all, " "), msg)
	}
	return out, nil
}

func adbCredentialsPath() string {
	if isolatedADB != nil {
		return filepath.Join(isolatedADB.root, "home", ".android", "adbkey")
	}
	// adb keeps its key in ANDROID_USER_HOME, else ~/.android (on Windows
	// %USERPROFILE%\.android, which os.UserHomeDir returns).
	if dir := os.Getenv("ANDROID_USER_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "adbkey")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".android", "adbkey")
}

// UseSystem reuses the standard adb server and key store. The key contents are
// never read into the WebView or persisted by the app.
func (a *adbService) UseSystem() (adbAuthResult, error) {
	bin, resolveErr := resolveADB()
	if resolveErr != nil {
		return adbAuthResult{Mode: "system"}, resolveErr
	}
	cmd := adbCommand(context.Background(), bin, "start-server")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return adbAuthResult{Mode: "system", Output: strings.TrimSpace(string(out))}, i18n.Wrap(err, "adb.error.startServer")
	}
	devices, err := a.Devices()
	if err != nil {
		return adbAuthResult{Mode: "system", Output: strings.TrimSpace(string(out))}, err
	}
	for _, d := range devices {
		if d.State == "device" {
			return adbAuthResult{Mode: "system", CredentialsPath: adbCredentialsPath(), Serial: d.Serial, Output: strings.TrimSpace(string(out)), Message: i18n.T("adb.msg.reusedCredentials")}, nil
		}
	}
	return adbAuthResult{Mode: "system", CredentialsPath: adbCredentialsPath(), Output: strings.TrimSpace(string(out)), Message: i18n.T("adb.msg.serverStarted")}, nil
}

// Pair starts Android wireless-debugging pairing. The pairing code is passed
// only to the adb subprocess and is never stored.
func (a *adbService) Pair(address, code string) (adbAuthResult, error) {
	address = strings.TrimSpace(address)
	code = strings.TrimSpace(code)
	if address == "" || len(address) > 128 || strings.ContainsAny(address, " \t\r\n") {
		return adbAuthResult{Mode: "pair"}, i18n.New("adb.error.badPairAddress")
	}
	if code == "" || len(code) > 32 || strings.ContainsAny(code, " \t\r\n") {
		return adbAuthResult{Mode: "pair"}, i18n.New("adb.error.badPairCode")
	}
	bin, resolveErr := resolveADB()
	if resolveErr != nil {
		return adbAuthResult{Mode: "pair"}, resolveErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := adbCommand(ctx, bin, "pair", address, code).CombinedOutput()
	result := adbAuthResult{Mode: "pair", CredentialsPath: adbCredentialsPath(), Output: strings.TrimSpace(string(out))}
	if err != nil {
		result.Message = i18n.T("adb.msg.pairFailed")
		return result, i18n.Wrap(errors.New(result.Output), "adb.error.pair")
	}
	result.Message = i18n.T("adb.msg.paired")
	return result, nil
}

func (a *adbService) Devices() ([]adbDevice, error) {
	status := a.TransportStatus()
	if status.Mode == "direct-tcp" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := a.directSession(ctx, status.Address); err != nil {
			return nil, err
		}
		return []adbDevice{{Serial: status.Address, State: "device", Model: "TCP ADB"}}, nil
	}
	if status.Mode == "direct-usb" {
		return a.DirectDevices()
	}
	out, err := adbRun("", "devices", "-l")
	if err != nil {
		return nil, err
	}
	var devices []adbDevice
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 || f[0] == "List" {
			continue
		}
		d := adbDevice{Serial: f[0], State: f[1]}
		for _, token := range f[2:] {
			if strings.HasPrefix(token, "model:") {
				d.Model = strings.TrimPrefix(token, "model:")
			}
			if strings.HasPrefix(token, "product:") {
				d.Product = strings.TrimPrefix(token, "product:")
			}
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func safeRemotePart(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\\x00\n\r") {
		return false
	}
	// A device file name becomes a local one: on Windows, refuse names the
	// file system would read as a stream, a device or another name.
	if runtime.GOOS == "windows" && (strings.ContainsAny(s, `:*?"<>|`) || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") || !filepath.IsLocal(s)) {
		return false
	}
	return true
}
func safePackage(s string) bool {
	if s == "" || len(s) > 180 {
		return false
	}
	for _, r := range s {
		if !(r == '.' || r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func hexAccount(s string) bool {
	s = strings.TrimSuffix(s, "/")
	if len(s) < 16 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
func (a *adbService) resourceOutput() (string, error) {
	a.resources.mu.RLock()
	root := a.resources.path
	a.resources.mu.RUnlock()
	if root == "" {
		return "", i18n.New("resources.error.notChosen")
	}
	if st := inspectResourceDir(root); !st.Exists || !st.IsDirectory {
		return "", errors.New(st.Message)
	}
	out := filepath.Join(root, "android", "adb")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	return out, nil
}

// Pull scans the app's external files directory and copies bounded candidate files locally.
// The command uses exec-out/cat instead of relying on a host-side adb pull path, which works
// with the same shell access path used by StarMoe-box/Shizuku.
func (a *adbService) Pull(serial, packageName string) (adbPullResult, error) {
	started := time.Now()
	result := adbPullResult{Serial: serial, Package: packageName, StartedAt: started.Format(time.RFC3339Nano)}
	if !safePackage(packageName) {
		return result, i18n.New("adb.error.badPackage")
	}
	outDir, err := a.resourceOutput()
	if err != nil {
		return result, err
	}
	result.Output = outDir
	result.Root = "/sdcard/Android/data/" + packageName + "/files"
	top, err := a.run(serial, "shell", "ls", "-1p", result.Root)
	if err != nil {
		return result, err
	}
	var accounts []string
	for _, line := range strings.Split(string(top), "\n") {
		name := strings.TrimSpace(line)
		if hexAccount(name) {
			accounts = append(accounts, strings.TrimSuffix(name, "/"))
		}
	}
	sort.Strings(accounts)
	if len(accounts) == 0 {
		result.Message = i18n.T("adb.msg.noAccounts")
		result.FinishedAt = time.Now().Format(time.RFC3339Nano)
		return result, nil
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, account := range accounts {
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			remote := result.Root + "/" + account
			listing, e := a.run(serial, "shell", "find", remote, "-type", "f", "-maxdepth", "2", "-printf", "'%p\\t%s\\n'")
			if e != nil {
				// Some Android toybox versions lack -printf; fall back to one-level ls.
				listing, e = a.run(serial, "shell", "ls", "-1p", remote)
				if e != nil {
					mu.Lock()
					result.Errors = append(result.Errors, account+": "+e.Error())
					mu.Unlock()
					return
				}
				for _, line := range strings.Split(string(listing), "\n") {
					name := strings.TrimSpace(strings.TrimSuffix(line, "/"))
					if !safeRemotePart(name) || strings.HasSuffix(line, "/") {
						continue
					}
					a.pullOne(serial, packageName, account, remote+"/"+name, name, outDir, &result, &mu)
				}
				return
			}
			for _, line := range strings.Split(string(listing), "\n") {
				p := strings.SplitN(strings.TrimSpace(line), "\t", 2)
				if len(p) != 2 {
					continue
				}
				size, pe := strconv.ParseInt(strings.TrimSpace(p[1]), 10, 64)
				if pe != nil || size <= 0 || size > adbMaxFile {
					mu.Lock()
					result.Skipped++
					mu.Unlock()
					continue
				}
				remoteFile := p[0]
				base := filepath.Base(remoteFile)
				if !safeRemotePart(base) {
					mu.Lock()
					result.Skipped++
					mu.Unlock()
					continue
				}
				a.pullOne(serial, packageName, account, remoteFile, base, outDir, &result, &mu)
			}
		}()
	}
	wg.Wait()
	result.FinishedAt = time.Now().Format(time.RFC3339Nano)
	result.Message = i18n.T("adb.msg.pulled", "accounts", len(accounts), "files", result.Files)
	report, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(filepath.Join(outDir, "scan-report.json"), append(report, '\n'), 0o644)
	return result, nil
}

func (a *adbService) pullOne(serial, packageName, account, remote, name, outDir string, result *adbPullResult, mu *sync.Mutex) {
	data, err := a.run(serial, "exec-out", "cat", remote)
	if err != nil || int64(len(data)) > adbMaxFile || len(data) == 0 {
		mu.Lock()
		result.Skipped++
		if err != nil {
			result.Errors = append(result.Errors, remote+": "+err.Error())
		}
		mu.Unlock()
		return
	}
	local := filepath.Join(outDir, account+"_"+name)
	if err := os.WriteFile(local, data, 0o600); err != nil {
		mu.Lock()
		result.Errors = append(result.Errors, local+": "+err.Error())
		mu.Unlock()
		return
	}
	meta := map[string]any{"package": packageName, "serial": serial, "account": account, "path": remote, "size": len(data), "pulledAt": time.Now().Format(time.RFC3339Nano), "file": filepath.Base(local)}
	if b, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(local+".json", append(b, '\n'), 0o644)
	}
	mu.Lock()
	result.Files++
	result.Bytes += int64(len(data))
	mu.Unlock()
}

// PullResources mirrors runtime Master and encrypted bundle caches into the local
// android tree. The unpacker can consume these files after transport completes.
func (a *adbService) PullResources(serial, packageName string) (adbPullResult, error) {
	if a.TransportStatus().Mode != "system" {
		return a.directResources(serial, packageName)
	}
	result := adbPullResult{Serial: serial, Package: packageName, StartedAt: time.Now().Format(time.RFC3339Nano)}
	if !safePackage(packageName) {
		return result, i18n.New("adb.error.badPackage")
	}
	out, err := a.resourceOutput()
	if err != nil {
		return result, err
	}
	result.Output = filepath.Dir(out)
	result.Root = "/sdcard/Android/data/" + packageName + "/files"
	bin, resolveErr := resolveADB()
	if resolveErr != nil {
		return result, resolveErr
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, dir := range []string{"Master", "EncryptedBundles", "RemoteCatalog"} {
		dir := dir
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := filepath.Join(result.Output, dir)
			if err := os.MkdirAll(target, 0o755); err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, err.Error())
				mu.Unlock()
				return
			}
			args := []string{}
			if serial != "" {
				args = append(args, "-s", serial)
			}
			args = append(args, "pull", result.Root+"/"+dir+"/.", target)
			cmd := adbCommand(context.Background(), bin, args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if _, err := cmd.Output(); err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, dir+": "+strings.TrimSpace(stderr.String()))
				mu.Unlock()
				return
			}
			_ = filepath.WalkDir(target, func(_ string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err == nil {
					mu.Lock()
					result.Files++
					result.Bytes += info.Size()
					mu.Unlock()
				}
				return nil
			})
		}()
	}
	wg.Wait()
	result.FinishedAt = time.Now().Format(time.RFC3339Nano)
	result.Message = i18n.T("adb.msg.resourcesPulled", "files", result.Files)
	b, _ := json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(filepath.Join(result.Output, "resource-pull-report.json"), append(b, '\n'), 0o644)
	return result, nil
}

// Game package variants, as stable identifiers (see gamePackageVariantLabel).
const (
	variantJP       = "jp"
	variantIntl     = "intl"
	variantBili     = "bilibili"
	variantRegional = "regional"
)

// gamePackageVariantLabel names a variant in the current language.
func gamePackageVariantLabel(variant string) string {
	return i18n.T("package.variant." + variant)
}

// gamePackageVariant limits device discovery to Sirius package families. It
// returns the variant identifier, or "" for other packages.
func gamePackageVariant(name string) string {
	switch name {
	case "com.bushiroad.sirius":
		return variantJP
	case "com.bushiroad.sirius.en", "com.bushiroad.sirius.global":
		return variantIntl
	case "com.bilibili.sirius", "com.bilibili.sirius.official":
		return variantBili
	}
	for _, family := range []string{"com.bushiroad.sirius", "com.bilibili.sirius"} {
		if strings.HasPrefix(name, family+".") && safePackage(name) {
			return variantRegional
		}
	}
	return ""
}

// Packages scans storage and returns only the game's package families.
func (a *adbService) Packages(serial string) ([]adbPackage, error) {
	listing, err := a.run(serial, "shell", "ls", "-1p", "/sdcard/Android/data")
	if err != nil {
		return nil, err
	}

	var out []adbPackage
	seen := map[string]bool{}
	withFiles := map[string]bool{}
	// One find call avoids issuing hundreds of shell processes for every system package.
	if filesListing, e := a.run(serial, "shell", "find", "/sdcard/Android/data", "-mindepth", "2", "-maxdepth", "2", "-type", "d", "-name", "files"); e == nil {
		for _, line := range strings.Split(string(filesListing), "\n") {
			parts := strings.Split(strings.TrimSpace(line), "/")
			if len(parts) >= 6 && parts[len(parts)-1] == "files" && safePackage(parts[len(parts)-2]) {
				withFiles[parts[len(parts)-2]] = true
			}
		}
	}
	for _, line := range strings.Split(string(listing), "\n") {
		name := strings.TrimSuffix(strings.TrimSpace(line), "/")
		if !safePackage(name) || seen[name] {
			continue
		}
		seen[name] = true
		variant := gamePackageVariant(name)
		if variant == "" {
			continue
		}
		root := "/sdcard/Android/data/" + name + "/files"
		out = append(out, adbPackage{Package: name, Root: root, Variant: variant, HasFiles: withFiles[name]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Package < out[j].Package
	})
	return out, nil
}
