package main

import (
	"os"
	"testing"
)

func TestADBTransportPersistence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &adbService{}
	if _, err := a.SetTransport("direct-tcp", "192.168.1.20:37123"); err != nil {
		t.Fatal(err)
	}
	b := &adbService{}
	b.loadADBTransport()
	s := b.TransportStatus()
	if s.Mode != "direct-tcp" || s.Address != "192.168.1.20:37123" || s.Connected {
		t.Fatalf("bad restored status: %+v", s)
	}
	if _, err := b.SetTransport("direct-tcp", "missing-port"); err == nil {
		t.Fatal("accepted invalid TCP address")
	}
	c := &adbService{}
	c.loadADBTransport()
	if c.TransportStatus().Address != s.Address {
		t.Fatal("invalid input overwrote saved configuration")
	}
	if err := os.WriteFile(adbTransportPath(), []byte(`{"mode":"unknown"}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := &adbService{}
	d.loadADBTransport()
	if d.TransportStatus().Mode != "system" {
		t.Fatal("invalid saved mode accepted")
	}
	if err := os.WriteFile(adbTransportPath(), []byte(`{"mode":"direct-usb"}`), 0600); err != nil {
		t.Fatal(err)
	}
	e := &adbService{}
	e.loadADBTransport()
	want := "system"
	if directUSBAvailable {
		want = "direct-usb"
	}
	if e.TransportStatus().Mode != want {
		t.Fatalf("saved direct USB restored as %q, want %q", e.TransportStatus().Mode, want)
	}
	if _, err := e.SetTransport("direct-usb", ""); (err == nil) != directUSBAvailable {
		t.Fatalf("direct USB selection: %v (available %v)", err, directUSBAvailable)
	}
}
