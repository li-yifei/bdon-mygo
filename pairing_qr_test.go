package main

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPairQRIsEphemeralPNG(t *testing.T) {
	a := &adbService{}
	r, err := a.PairQR()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Payload, "WIFI:T:ADB;S:"+product.PairingServicePrefix) || !strings.HasSuffix(r.Payload, ";;") {
		t.Fatalf("invalid payload format")
	}
	png, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.DataURL, "data:image/png;base64,"))
	if err != nil || string(png[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("expected valid PNG")
	}
	st, err := a.PairQRStatus(r.SessionID)
	if err != nil || st.SessionID != r.SessionID {
		t.Fatal("session status missing")
	}
	qrPairs.RLock()
	s := qrPairs.items[r.SessionID]
	qrPairs.RUnlock()
	if err := a.PairQRCancel(r.SessionID); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	code := s.code
	s.mu.Unlock()
	if code != "" {
		t.Fatal("cancel retained secret")
	}
	if _, err := a.PairQRStatus(r.SessionID); err == nil {
		t.Fatal("cancel retained session")
	}
}

func TestQRMDNSHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := qrMDNS(ctx, "_adb-tls-pairing._tcp", "missing", ""); err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancel blocked")
	}
}

// Opt-in because this test uses the host multicast interfaces.
func TestQRMDNSLiveMissingServiceWaitsForDeadline(t *testing.T) {
	if os.Getenv("OURNOTES_TEST_MDNS") != "1" {
		t.Skip("set OURNOTES_TEST_MDNS=1 for live multicast test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := qrMDNS(ctx, "_ournotes-missing-test._tcp", "", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("discovery failed before timeout: %v", err)
	}
	if time.Since(started) < 1500*time.Millisecond {
		t.Fatalf("discovery returned too soon: %v", time.Since(started))
	}
}
