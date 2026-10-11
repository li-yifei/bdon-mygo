package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pgaskin/go-adb/adb/adbproto/atransport"
	"github.com/pgaskin/go-adb/adblib"
	"github.com/pgaskin/go-adb/adblib/adbexec"
	"github.com/pgaskin/go-adb/adblib/adbsync"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

type adbTransportStatus struct {
	Mode      string `json:"mode"`
	Address   string `json:"address"`
	Connected bool   `json:"connected"`
}

func (a *adbService) TransportStatus() adbTransportStatus {
	a.transportMu.Lock()
	defer a.transportMu.Unlock()
	mode := a.mode
	if mode == "" {
		mode = "system"
	}
	connected := a.direct != nil
	if connected {
		select {
		case <-a.direct.Kicked():
			connected = false
		default:
		}
	}
	return adbTransportStatus{Mode: mode, Address: a.address, Connected: connected}
}
func adbTransportPath() string {
	if isolatedADB != nil {
		return filepath.Join(isolatedADB.root, "adb-transport.json")
	}
	p, _ := productConfigPath("adb-transport.json")
	return p
}
func (a *adbService) loadADBTransport() {
	if isolatedADB != nil {
		a.mode = "system"
		return
	}
	b, err := os.ReadFile(adbTransportPath())
	if err != nil {
		return
	}
	var saved adbTransportStatus
	if json.Unmarshal(b, &saved) == nil && validADBTransport(saved.Mode, saved.Address) == nil {
		a.mode, a.address = saved.Mode, saved.Address
	}
}
func validADBTransport(mode, address string) error {
	if isolatedADB != nil && mode != "system" {
		return i18n.New("transport.error.isolatedWireless")
	}
	if mode != "system" && mode != "direct-usb" && mode != "direct-tcp" {
		return i18n.New("transport.error.badMode")
	}
	if mode == "direct-usb" && !directUSBAvailable {
		return i18n.New("transport.error.noUSBDriver")
	}
	if mode == "direct-tcp" {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" || port == "" {
			return i18n.New("transport.error.needAddress")
		}
	}
	return nil
}
func (a *adbService) SetTransport(mode, address string) (adbTransportStatus, error) {
	address = strings.TrimSpace(address)
	if err := validADBTransport(mode, address); err != nil {
		return adbTransportStatus{}, err
	}
	p := adbTransportPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return adbTransportStatus{}, err
	}
	b, _ := json.Marshal(adbTransportStatus{Mode: mode, Address: address})
	f, err := os.CreateTemp(filepath.Dir(p), ".adb-transport-")
	if err != nil {
		return adbTransportStatus{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), p)
	}
	if err != nil {
		return adbTransportStatus{}, err
	}
	a.transportMu.Lock()
	if a.direct != nil {
		a.direct.Kick(nil)
		a.direct = nil
	}
	a.mode = mode
	a.address = strings.TrimSpace(address)
	a.directSerial = ""
	a.transportMu.Unlock()
	return a.TransportStatus(), nil
}
func (a *adbService) ReleaseDirect() (adbTransportStatus, error) { return a.SetTransport("system", "") }
func (a *adbService) ReleaseSystemDevice(serial string) (string, error) {
	if isolatedADB != nil {
		return "", i18n.New("transport.error.isolatedWireless")
	}
	if serial == "" {
		return "", i18n.New("transport.error.selectUSB")
	}
	out, err := adbRun(serial, "detach")
	return string(out), err
}
func (a *adbService) directSession(ctx context.Context, serial string) (*atransport.Transport, error) {
	a.transportMu.Lock()
	defer a.transportMu.Unlock()
	if a.direct != nil && a.directSerial == serial {
		select {
		case <-a.direct.Kicked():
			a.direct = nil
		default:
			return a.direct, nil
		}
	}
	if a.direct != nil {
		a.direct.Kick(nil)
		a.direct = nil
	}
	var t *atransport.Transport
	var err error
	if a.mode == "direct-tcp" {
		t, err = adblib.ConnectTCP(ctx, a.address, nil)
	} else {
		t, err = connectDirectUSB(ctx, serial)
	}
	if err != nil {
		return nil, err
	}
	a.direct = t
	a.directSerial = serial
	return t, nil
}
func (a *adbService) run(serial string, args ...string) ([]byte, error) {
	if a.TransportStatus().Mode == "system" {
		return adbRun(serial, args...)
	}
	if len(args) < 2 || (args[0] != "shell" && args[0] != "exec-out") {
		return nil, i18n.New("transport.error.deviceServiceOnly")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t, err := a.directSession(ctx, serial)
	if err != nil {
		return nil, err
	}
	argv := append([]string(nil), args[1:]...)
	// Existing caller wraps find's format for the adb remote shell. Normalize
	// before quoting each argument for direct exec.
	for i, s := range argv {
		if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
			argv[i] = s[1 : len(s)-1]
		}
	}
	return adbexec.Output(ctx, t, adbexec.Quote(argv...), nil)
}
func (a *adbService) directResources(serial, pkg string) (adbPullResult, error) {
	output, err := a.resourceOutput()
	if err != nil {
		return adbPullResult{}, err
	}
	return a.directResourceDirectories(serial, pkg, []string{"Master", "EncryptedBundles", "RemoteCatalog"}, filepath.Dir(output))
}
func (a *adbService) directResourceDirectories(serial, pkg string, directories []string, output string) (adbPullResult, error) {
	r := adbPullResult{Serial: serial, Package: pkg, StartedAt: time.Now().Format(time.RFC3339Nano), Output: output}
	if !safePackage(pkg) {
		return r, i18n.New("adb.error.badPackage")
	}

	r.Root = "/sdcard/Android/data/" + pkg + "/files"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	t, err := a.directSession(ctx, serial)
	if err != nil {
		return r, err
	}
	client := &adbsync.Client{Server: t, MaxConns: 2, IdleConnTimeout: time.Second}
	var walk func(string, string, int) error
	walk = func(remote, local string, depth int) error {
		if depth > 32 {
			return i18n.New("transport.error.tooDeep")
		}
		entries, e := client.ReadDir(ctx, remote)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(local, 0755); e != nil {
			return e
		}
		for _, entry := range entries {
			if !safeRemotePart(entry.Name()) {
				continue
			}
			dest := filepath.Join(local, entry.Name())
			src := remote + "/" + entry.Name()
			if entry.IsDir() {
				if e = walk(src, dest, depth+1); e != nil {
					return e
				}
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			in, e := client.Open(ctx, src)
			if e != nil {
				return e
			}
			f, e := os.CreateTemp(local, ".pull-")
			if e != nil {
				in.Close()
				return e
			}
			n, e := io.Copy(f, io.LimitReader(in, (512<<20)+1))
			in.Close()
			ce := f.Close()
			if e == nil {
				e = ce
			}
			if n > 512<<20 {
				e = i18n.New("transport.error.fileTooLarge")
			}
			if e == nil {
				e = os.Rename(f.Name(), dest)
			}
			if e != nil {
				os.Remove(f.Name())
				return e
			}
			r.Files++
			r.Bytes += n
		}
		return nil
	}
	for _, dir := range directories {
		if e := walk(r.Root+"/"+dir, filepath.Join(r.Output, dir), 0); e != nil {
			r.Errors = append(r.Errors, dir+": "+e.Error())
		}
	}
	r.FinishedAt = time.Now().Format(time.RFC3339Nano)
	r.Message = i18n.T("adb.msg.resourcesPulled", "files", r.Files)
	return r, nil
}
