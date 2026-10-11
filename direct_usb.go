//go:build cgo

package main

import (
	"context"
	"crypto"
	"io"
	"sync"

	"github.com/google/gousb"
	"github.com/pgaskin/go-adb/adb/adbhost"
	"github.com/pgaskin/go-adb/adb/adbproto/aproto"
	"github.com/pgaskin/go-adb/adb/adbproto/atransport"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// directUSBAvailable reports whether this build has the Go direct USB
// transport (gousb, built with cgo and libusb): the macOS release.
const directUSBAvailable = true

type usbADBPipe struct {
	ctx     context.Context
	cancel  context.CancelFunc
	usb     *gousb.Context
	dev     *gousb.Device
	config  *gousb.Config
	intf    *gousb.Interface
	in      *gousb.InEndpoint
	out     *gousb.OutEndpoint
	once    sync.Once
	readMu  sync.Mutex
	writeMu sync.Mutex
	pending []byte
	readErr error
}

func (p *usbADBPipe) Read(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	if len(p.pending) == 0 && p.readErr != nil {
		return 0, p.readErr
	}
	// USB bulk reads need a buffer covering complete packets. Preserve the
	// remainder when the protocol requests a 24-byte header.
	for len(p.pending) == 0 {
		packet := p.in.Desc.MaxPacketSize
		size := ((len(b) + packet - 1) / packet) * packet
		buf := make([]byte, size)
		n, err := p.in.ReadContext(p.ctx, buf)
		if n == 0 && err != nil {
			return 0, err
		}
		p.pending = buf[:n]
		p.readErr = err
	}
	n := copy(b, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}
func (p *usbADBPipe) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	n, err := p.out.WriteContext(p.ctx, b)
	if err != nil {
		return n, err
	}
	if n != len(b) {
		return n, io.ErrShortWrite
	}
	if n%p.out.Desc.MaxPacketSize == 0 {
		_, err = p.out.WriteContext(p.ctx, []byte{})
	}
	return n, err
}
func (p *usbADBPipe) Close() error {
	p.once.Do(func() { p.cancel(); p.intf.Close(); p.config.Close(); p.dev.Close(); p.usb.Close() })
	return nil
}
func adbUSBSetting(desc *gousb.DeviceDesc) (int, gousb.InterfaceSetting, bool) {
	for n, c := range desc.Configs {
		for _, i := range c.Interfaces {
			for _, s := range i.AltSettings {
				if s.Class == 0xff && s.SubClass == 0x42 && s.Protocol == 1 {
					return n, s, true
				}
			}
		}
	}
	return 0, gousb.InterfaceSetting{}, false
}
func (a *adbService) DirectDevices() ([]adbDevice, error) {
	u := gousb.NewContext()
	defer u.Close()
	ds, err := u.OpenDevices(func(d *gousb.DeviceDesc) bool { _, _, ok := adbUSBSetting(d); return ok })
	result := []adbDevice{}
	for _, d := range ds {
		serial, e := d.SerialNumber()
		if e == nil {
			result = append(result, adbDevice{Serial: serial, State: "usb", Model: "USB ADB"})
		}
		d.Close()
	}
	return result, err
}
func connectDirectUSB(ctx context.Context, serial string) (*atransport.Transport, error) {
	key, err := adbhost.LoadUserKey()
	if err != nil {
		return nil, err
	}
	u := gousb.NewContext()
	ds, err := u.OpenDevices(func(d *gousb.DeviceDesc) bool { _, _, ok := adbUSBSetting(d); return ok })
	var dev *gousb.Device
	for _, d := range ds {
		s, e := d.SerialNumber()
		if e == nil && s == serial && dev == nil {
			dev = d
		} else {
			d.Close()
		}
	}
	if dev == nil {
		u.Close()
		return nil, i18n.Wrap(err, "transport.error.usbOpen", "serial", serial)
	}
	n, setting, _ := adbUSBSetting(dev.Desc)
	cfg, err := dev.Config(n)
	if err != nil {
		dev.Close()
		u.Close()
		return nil, err
	}
	intf, err := cfg.Interface(setting.Number, setting.Alternate)
	if err != nil {
		cfg.Close()
		dev.Close()
		u.Close()
		return nil, i18n.Wrap(err, "transport.error.usbBusy")
	}
	readctx, cancel := context.WithCancel(context.Background())
	p := &usbADBPipe{ctx: readctx, cancel: cancel, usb: u, dev: dev, config: cfg, intf: intf}
	for _, e := range setting.Endpoints {
		if e.TransferType != gousb.TransferTypeBulk {
			continue
		}
		if e.Direction == gousb.EndpointDirectionIn {
			p.in, err = intf.InEndpoint(e.Number)
		} else {
			p.out, err = intf.OutEndpoint(e.Number)
		}
		if err != nil {
			p.Close()
			return nil, err
		}
	}
	if p.in == nil || p.out == nil {
		p.Close()
		return nil, i18n.New("transport.error.noBulkEndpoint")
	}
	t, err := atransport.Connect(aproto.New(p), &atransport.Config{Keys: []crypto.Signer{key}})
	if err != nil {
		p.Close()
		return nil, err
	}
	if err = t.WaitConnected(ctx); err != nil {
		t.Kick(err)
		return nil, err
	}
	return t, nil
}
