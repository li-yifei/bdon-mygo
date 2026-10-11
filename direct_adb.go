package main

// Optional direct ADB transport. It talks to adbd over USB without starting
// or contacting an adb host server. The transport and RSA authentication are
// provided by pgaskin/go-adb; the rest of the app keeps using the same pull
// result format as the CLI-backed path.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pgaskin/go-adb/adblib"
	"github.com/pgaskin/go-adb/adblib/adbexec"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

type directADBResult struct {
	Mode    string `json:"mode"`
	Serial  string `json:"serial"`
	Package string `json:"package,omitempty"`
	Files   int    `json:"files,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Message string `json:"message"`
}

// DirectProbe authenticates with the system key and runs getprop through the
// USB transport, proving that no local adb server is involved.
func (a *adbService) DirectProbe(serial string) (directADBResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t, err := connectDirectUSB(ctx, serial)
	if err != nil {
		return directADBResult{Mode: "direct-usb", Serial: serial}, err
	}
	defer t.Kick(nil)
	out, err := adbexec.Output(ctx, t, "getprop ro.product.model", nil)
	if err != nil {
		return directADBResult{Mode: "direct-usb", Serial: serial}, err
	}
	return directADBResult{Mode: "direct-usb", Serial: serial, Message: i18n.T("transport.msg.usbConnected", "output", strings.TrimSpace(string(out)))}, nil
}

// DirectTCPProbe connects to legacy ADB-over-TCP or an already-connected
// wireless-debugging endpoint without an adb host server.
func (a *adbService) DirectTCPProbe(address string) (directADBResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t, err := adblib.ConnectTCP(ctx, address, nil)
	if err != nil {
		return directADBResult{Mode: "direct-tcp", Serial: address}, err
	}
	defer t.Kick(nil)
	out, err := adbexec.Output(ctx, t, "getprop ro.product.model", nil)
	if err != nil {
		return directADBResult{Mode: "direct-tcp", Serial: address}, err
	}
	return directADBResult{Mode: "direct-tcp", Serial: address, Message: i18n.T("transport.msg.tcpConnected", "output", strings.TrimSpace(string(out)))}, nil
}

// DirectShell is exposed for diagnostics and future direct pull operations.
func (a *adbService) DirectShell(serial, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", i18n.New("transport.error.emptyShell")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t, err := connectDirectUSB(ctx, serial)
	if err != nil {
		return "", err
	}
	defer t.Kick(nil)
	out, err := adbexec.Output(ctx, t, command, nil)
	return string(out), err
}

// DirectPull uses the same bounded file policy as Pull, with shell/cat routed
// through the direct USB transport. It is intentionally opt-in while TCP and
// wireless-direct transports are added later.
func (a *adbService) DirectPull(serial, packageName string) (adbPullResult, error) {
	if !safePackage(packageName) {
		return adbPullResult{}, i18n.New("adb.error.badPackage")
	}
	outDir, err := a.resourceOutput()
	if err != nil {
		return adbPullResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t, err := connectDirectUSB(ctx, serial)
	if err != nil {
		return adbPullResult{Serial: serial, Package: packageName}, err
	}
	defer t.Kick(nil)
	root := "/sdcard/Android/data/" + packageName + "/files"
	listing, err := adbexec.Output(ctx, t, "ls -1p "+adbexec.Quote(root), nil)
	if err != nil {
		return adbPullResult{}, err
	}
	var candidates []byte
	for _, name := range strings.Split(string(listing), "\n") {
		account := strings.TrimSuffix(strings.TrimSpace(name), "/")
		if !hexAccount(account) {
			continue
		}
		part, e := adbexec.Output(ctx, t, "find "+adbexec.Quote(root+"/"+account)+" -maxdepth 2 -type f -printf '%p\\t%s\\n'", nil)
		if e != nil {
			return adbPullResult{}, e
		}
		candidates = append(candidates, part...)
	}
	listing = candidates
	if err != nil {
		return adbPullResult{Serial: serial, Package: packageName, Root: root}, err
	}
	result := adbPullResult{Serial: serial, Package: packageName, Root: root, Output: outDir, StartedAt: time.Now().Format(time.RFC3339Nano)}
	for _, line := range strings.Split(string(listing), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(parts) != 2 {
			continue
		}
		size, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || size <= 0 || size > adbMaxFile {
			result.Skipped++
			continue
		}
		data, e := adbexec.Output(ctx, t, "cat "+adbexec.Quote(parts[0]), nil)
		if e != nil || int64(len(data)) > adbMaxFile || len(data) == 0 {
			result.Skipped++
			continue
		}
		name := filepath.Base(parts[0])
		if !safeRemotePart(name) {
			result.Skipped++
			continue
		}
		if e = os.WriteFile(filepath.Join(outDir, "direct_"+name), data, 0o600); e != nil {
			result.Errors = append(result.Errors, e.Error())
			continue
		}
		result.Files++
		result.Bytes += int64(len(data))
	}
	result.FinishedAt = time.Now().Format(time.RFC3339Nano)
	result.Message = i18n.T("transport.msg.pulled", "files", result.Files)
	return result, nil
}
