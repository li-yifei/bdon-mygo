package main

import (
	"os"
	"strings"
	"testing"
)

func TestADBLivePull(t *testing.T) {
	if os.Getenv("OURNOTES_TEST_ADB_SERIAL") == "" {
		t.Skip("set OURNOTES_TEST_ADB_SERIAL for live device check")
	}
	a := &adbService{resources: &resourceService{path: t.TempDir()}}
	result, err := a.Pull(os.Getenv("OURNOTES_TEST_ADB_SERIAL"), "com.bushiroad.sirius")
	if err != nil {
		t.Fatal(err)
	}
	if result.Files == 0 {
		t.Fatalf("no candidates: %+v", result)
	}
	if result.Bytes == 0 {
		t.Fatalf("no bytes: %+v", result)
	}
	t.Logf("files=%d bytes=%d skipped=%d errors=%v", result.Files, result.Bytes, result.Skipped, result.Errors)
}

func TestADBPackagesLive(t *testing.T) {
	if os.Getenv("OURNOTES_TEST_ADB_SERIAL") == "" {
		t.Skip("set OURNOTES_TEST_ADB_SERIAL for live device check")
	}
	a := &adbService{}
	ps, err := a.Packages(os.Getenv("OURNOTES_TEST_ADB_SERIAL"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		t.Logf("%s %s files=%v", p.Package, p.Variant, p.HasFiles)
	}
}

func TestADBSystemAuthLive(t *testing.T) {
	if os.Getenv("OURNOTES_TEST_ADB_SERIAL") == "" {
		t.Skip("set OURNOTES_TEST_ADB_SERIAL for live device check")
	}
	a := &adbService{}
	result, err := a.UseSystem()
	if err != nil {
		t.Fatal(err)
	}
	if result.Serial == "" {
		t.Fatalf("system auth did not find device: %+v", result)
	}
	t.Logf("mode=%s serial=%s credentials=%s", result.Mode, result.Serial, result.CredentialsPath)
}

func TestADBDirectUSBProbeLive(t *testing.T) {
	serial := os.Getenv("OURNOTES_TEST_ADB_SERIAL")
	if serial == "" {
		t.Skip("set OURNOTES_TEST_ADB_SERIAL for live device check")
	}
	result, err := (&adbService{}).DirectProbe(serial)
	if err != nil && strings.Contains(err.Error(), "unsupported operation") {
		t.Skip("go-adb direct USB backend is unavailable on this host")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mode=%s serial=%s message=%s", result.Mode, result.Serial, result.Message)
}

func TestADBDirectPullLive(t *testing.T) {
	serial := os.Getenv("OURNOTES_TEST_ADB_SERIAL")
	if serial == "" {
		t.Skip("live USB serial required")
	}
	a := &adbService{resources: &resourceService{path: t.TempDir()}}
	r, err := a.DirectPull(serial, "com.bushiroad.sirius")
	if err != nil {
		t.Fatal(err)
	}
	if r.Files == 0 || len(r.Errors) > 0 {
		t.Fatalf("direct pull failed: %+v", r)
	}
	t.Logf("files=%d bytes=%d", r.Files, r.Bytes)
}

func TestADBDirectTransportPullLive(t *testing.T) {
	serial := os.Getenv("OURNOTES_TEST_ADB_SERIAL")
	if serial == "" {
		t.Skip("live USB serial required")
	}
	a := &adbService{resources: &resourceService{path: t.TempDir()}}
	a.SetTransport("direct-usb", "")
	defer a.ReleaseDirect()
	ps, err := a.Packages(serial)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Package == "com.bushiroad.sirius" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing Sirius package")
	}
	r, err := a.Pull(serial, "com.bushiroad.sirius")
	if err != nil {
		t.Fatal(err)
	}
	if r.Files != 3 || len(r.Errors) != 0 {
		t.Fatalf("unexpected direct results: %+v", r)
	}
	t.Logf("packages=%d files=%d bytes=%d", len(ps), r.Files, r.Bytes)
}
