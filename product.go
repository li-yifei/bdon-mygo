package main

import (
	_ "embed"
	"encoding/json"
)

// product.json is the single source of the product identity. Go, the MyGo
// config and the build scripts all read it, so a rename is one edit.
//
//go:embed product.json
var productJSON []byte

type productIdentity struct {
	Name                 string `json:"name"`                 // app, menu and window title
	Identifier           string `json:"identifier"`           // bundle identifier
	ConfigDir            string `json:"configDir"`            // directory name of the app's config, data, cache and logs (app_paths.go)
	CredentialsDir       string `json:"credentialsDir"`       // save.env directory: ~/.config/<dir> on macOS, else the config directory
	LegacyCredentialsDir string `json:"legacyCredentialsDir"` // previous ~/.config/<dir>/save.env, read as a fallback
	LegacyName           string `json:"legacyName"`           // previous app bundle name, source of the storage migration
	LegacyStorageDir     string `json:"legacyStorageDir"`     // previous Contents/Resources/<dir> in the bundle
	DevStorageDir        string `json:"devStorageDir"`        // development cache next to the source
	LinuxBinary          string `json:"linuxBinary"`          // Linux executable name
	PairingServicePrefix string `json:"pairingServicePrefix"` // ADB QR pairing service name prefix
	StateKeys            struct {
		Main     string `json:"main"`
		Settings string `json:"settings"`
	} `json:"stateKeys"`
}

var product = mustProduct(productJSON)

func mustProduct(data []byte) productIdentity {
	var p productIdentity
	if err := json.Unmarshal(data, &p); err != nil {
		panic("product.json: " + err.Error())
	}
	return p
}

// productPreload exposes display identity to the page scripts.
func productPreload() string {
	b, _ := json.Marshal(map[string]string{"name": product.Name})
	return "window.__ournotesProduct = " + string(b) + ";\n"
}
