//go:build !cgo

package main

import (
	"context"

	"github.com/pgaskin/go-adb/adb/adbproto/atransport"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// directUSBAvailable is false in builds without cgo (the Windows and Linux
// releases): the settings hide the direct USB transport, and a saved choice
// of it falls back to the ADB compatibility mode.
const directUSBAvailable = false

func connectDirectUSB(context.Context, string) (*atransport.Transport, error) {
	return nil, i18n.New("transport.error.noUSBDriver")
}
func (a *adbService) DirectDevices() ([]adbDevice, error) {
	return nil, i18n.New("transport.error.noUSBDriver")
}
