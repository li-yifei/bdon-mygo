package main

// Android 11+ wireless-debugging QR pairing.  The QR payload is the format
// understood by the system camera: WIFI:T:ADB;S:<mDNS name>;P:<one-time code>;;
// Pairing state is process memory only; the code and payload are erased when a
// session finishes, expires, or is cancelled.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

const qrPairTTL = 5 * time.Minute

type qrPairStart struct {
	SessionID string `json:"sessionId"`
	Service   string `json:"service"`
	Payload   string `json:"payload"`
	DataURL   string `json:"dataUrl"`
	Expires   string `json:"expires"`
}

type qrPairStatus struct {
	SessionID string `json:"sessionId"`
	State     string `json:"state"` // waiting, pairing, connecting, connected, failed, cancelled, expired
	Message   string `json:"message"`
	Serial    string `json:"serial,omitempty"`
	Expires   string `json:"expires,omitempty"`
}

type qrPairSession struct {
	mu      sync.Mutex
	service string
	code    string
	state   string
	message string
	serial  string
	expires time.Time
	cancel  context.CancelFunc
}

var qrPairs = struct {
	sync.RWMutex
	items map[string]*qrPairSession
}{items: make(map[string]*qrPairSession)}

func randomQRString(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + fmt.Sprintf("%x", b), nil
}

// PairQR creates an in-memory pairing session and a PNG data URL for the UI.
// It does not start an adb server or write the pairing code to disk.
func (a *adbService) PairQR() (qrPairStart, error) {
	service, err := randomQRString(product.PairingServicePrefix, 8)
	if err != nil {
		return qrPairStart{}, err
	}
	code, err := randomQRString("", 12)
	if err != nil {
		return qrPairStart{}, err
	}
	// QR scanner accepts this exact AOSP/Android Studio payload. S is an mDNS
	// instance name, rather than a Wi-Fi SSID.
	payload := "WIFI:T:ADB;S:" + service + ";P:" + code + ";;"
	png, err := qrcode.Encode(payload, qrcode.Medium, 512)
	if err != nil {
		return qrPairStart{}, err
	}
	id, err := randomQRString("", 16)
	if err != nil {
		return qrPairStart{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &qrPairSession{service: service, code: code, state: "waiting", message: i18n.T("qr.msg.scan"), expires: time.Now().Add(qrPairTTL), cancel: cancel}
	qrPairs.Lock()
	// Keep one active QR session: regenerating invalidates previous scans.
	for oldID, old := range qrPairs.items {
		old.cancel()
		old.mu.Lock()
		old.code = ""
		old.state = "cancelled"
		old.mu.Unlock()
		delete(qrPairs.items, oldID)
	}
	qrPairs.items[id] = s
	qrPairs.Unlock()
	go a.runQRPair(ctx, id, s)
	return qrPairStart{SessionID: id, Service: service, Payload: payload, DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), Expires: s.expires.Format(time.RFC3339)}, nil
}

func (a *adbService) runQRPair(ctx context.Context, id string, s *qrPairSession) {
	defer func() {
		// Keep a small terminal status for the UI, but remove secret material.
		s.mu.Lock()
		s.code = ""
		s.mu.Unlock()
	}()
	deadline, cancel := context.WithDeadline(ctx, s.expires)
	defer cancel()
	entry, err := qrMDNS(deadline, "_adb-tls-pairing._tcp", s.service, "")
	if err != nil {
		a.qrFail(s, err.Error())
		return
	}
	s.mu.Lock()
	if deadline.Err() != nil || s.state == "cancelled" {
		s.mu.Unlock()
		return
	}
	s.state, s.message = "pairing", i18n.T("qr.msg.pairing")
	code := s.code
	s.mu.Unlock()
	address := net.JoinHostPort(entryHost(entry), strconv.Itoa(entry.Port))
	if err = a.runPairCommand(deadline, address, code); err != nil {
		a.qrFail(s, i18n.T("qr.msg.pairFailed", "error", err.Error()))
		return
	}
	s.mu.Lock()
	if deadline.Err() != nil || s.state == "cancelled" {
		s.mu.Unlock()
		return
	}
	s.state, s.message = "connecting", i18n.T("qr.msg.connecting")
	s.mu.Unlock()
	connect, err := qrMDNS(deadline, "_adb-tls-connect._tcp", "", entryHost(entry))
	if err != nil {
		a.qrFail(s, i18n.T("qr.msg.connectServiceFailed", "error", err.Error()))
		return
	}
	connectAddr := net.JoinHostPort(entryHost(connect), strconv.Itoa(connect.Port))
	if err = a.runConnectCommand(deadline, connectAddr); err != nil {
		a.qrFail(s, i18n.T("qr.msg.connectFailed", "error", err.Error()))
		return
	}
	s.mu.Lock()
	if deadline.Err() != nil || s.state == "cancelled" {
		s.mu.Unlock()
		return
	}
	s.state, s.message, s.serial = "connected", i18n.T("qr.msg.connected"), connectAddr
	s.mu.Unlock()
	_ = id
}

func entryHost(e *mdns.ServiceEntry) string {
	if e.AddrV4 != nil {
		return e.AddrV4.String()
	}
	if e.Addr != nil {
		return e.Addr.String()
	}
	return e.Host
}

// qrMDNSInterfaces selects explicit IPv4 multicast routes. On macOS the
// default route can point at a VPN interface which cannot send multicast.
func qrMDNSInterfaces() ([]net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var usable []net.Interface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsLoopback() {
				usable = append(usable, iface)
				break
			}
		}
	}
	if len(usable) == 0 {
		return nil, i18n.New("qr.error.noNetwork")
	}
	return usable, nil
}

func qrMDNS(ctx context.Context, service, match, host string) (*mdns.ServiceEntry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		interfaces, err := qrMDNSInterfaces()
		if err != nil {
			return nil, err
		}
		entries := make(chan *mdns.ServiceEntry, 64)
		done := make(chan error, len(interfaces))
		for _, iface := range interfaces {
			iface := iface
			params := mdns.DefaultParams(service)
			params.Timeout = time.Second
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < params.Timeout {
				params.Timeout = time.Until(deadline)
			}
			params.Interface = &iface
			params.DisableIPv6 = true
			params.Entries = entries
			go func() { done <- mdns.Query(params) }()
		}
		pending, successful := len(interfaces), false
		var lastErr error
		for pending > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case e := <-entries:
				if e != nil && e.Port != 0 && entryHost(e) != "" && (match == "" || strings.HasPrefix(e.Name, match+".")) && (host == "" || entryHost(e) == host) {
					return e, nil
				}
			case err := <-done:
				pending--
				if err == nil {
					successful = true
				} else {
					lastErr = err
				}
			}
		}
		// A working interface with no advertisement is expected while awaiting a
		// QR scan. Keep querying until the session deadline. A failed VPN interface
		// does not cancel successful discovery on Wi-Fi.
		if !successful {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, i18n.Wrap(lastErr, "qr.error.discovery", "name", product.Name)
		}
	}
}

func (a *adbService) runPairCommand(ctx context.Context, address, code string) error {
	if strings.TrimSpace(code) == "" {
		return i18n.New("qr.error.codeExpired")
	}
	bin, err := resolveADB()
	if err != nil {
		return err
	}
	// Reuse the existing safe argv-based helper; the code never enters a shell.
	out, err := adbCommand(ctx, bin, "pair", address, code).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("adb pair: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *adbService) runConnectCommand(ctx context.Context, address string) error {
	bin, err := resolveADB()
	if err != nil {
		return err
	}
	out, err := adbCommand(ctx, bin, "connect", address).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("adb connect: %s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "connected to ") {
		return fmt.Errorf("adb connect: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (a *adbService) qrFail(s *qrPairSession, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == "cancelled" {
		return
	}
	if strings.Contains(message, "deadline exceeded") {
		s.state = "expired"
		message = i18n.T("qr.msg.expiredHelp")
	} else {
		s.state = "failed"
	}
	s.message = message
}

// PairQRStatus returns a session's current state without exposing the code.
func (a *adbService) PairQRStatus(id string) (qrPairStatus, error) {
	qrPairs.RLock()
	s := qrPairs.items[id]
	qrPairs.RUnlock()
	if s == nil {
		return qrPairStatus{}, i18n.New("qr.error.sessionGone")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().After(s.expires) && s.state == "waiting" {
		s.state, s.message = "expired", i18n.T("qr.msg.expired")
	}
	return qrPairStatus{SessionID: id, State: s.state, Message: s.message, Serial: s.serial, Expires: s.expires.Format(time.RFC3339)}, nil
}

// PairQRCancel stops discovery and drops all sensitive session state.
func (a *adbService) PairQRCancel(id string) error {
	qrPairs.Lock()
	s := qrPairs.items[id]
	delete(qrPairs.items, id)
	qrPairs.Unlock()
	if s == nil {
		return i18n.New("qr.error.sessionMissing")
	}
	s.mu.Lock()
	s.state, s.message, s.code = "cancelled", i18n.T("qr.msg.cancelled"), ""
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
