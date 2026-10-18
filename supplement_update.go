package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
	"github.com/li-yifei/bdon-mygo/internal/supplement"
)

// The page script merges the stored supplement into bdon.moe's data where
// that data lacks an entry (see internal/supplement).
//
//go:embed shell/supplement.js
var supplementScript string

// supplementPreload hands the stored supplement and its page script to the page.
func supplementPreload(storageRoot string) string {
	return supplement.Preload(storageRoot) + supplementScript + "\n"
}

// decryptMasterBin decrypts one Master .bin file with the save credentials.
func decryptMasterBin(data []byte) ([]byte, error) {
	key, magic, err := localSaveCredentials()
	if err != nil {
		return nil, err
	}
	if len(data) < 64 || !bytes.Equal(data[:32], magic[:]) {
		return nil, i18n.New("supplement.error.format")
	}
	var iv [32]byte
	copy(iv[:], data[32:64])
	plain, err := newOurNotesRijndael256(key).decrypt(data[64:], iv)
	if err != nil {
		return nil, i18n.Wrap(err, "supplement.error.decrypt")
	}
	return plain, nil
}

// UpdateGameData pulls the game Master from the device, rebuilds the
// supplement from it and reads the charts of new songs from bdon's chart
// site. A failed pull or decode keeps the previous supplement; a failed chart
// read keeps the new game data and reports the chart error.
func (a *adbService) UpdateGameData(serial, pkg string) (string, error) {
	if pkg != supplement.Package {
		return "", i18n.New("supplement.error.jpOnly")
	}
	a.resources.mu.RLock()
	root := a.resources.path
	a.resources.mu.RUnlock()
	// The pull is a temporary stage in the cache; Build writes into root.
	stage, err := cacheStage(".event-pull-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if a.TransportStatus().Mode == "system" {
		bin, e := resolveADB()
		if e != nil {
			return "", e
		}
		args := []string{"-s", serial, "pull", "/sdcard/Android/data/" + pkg + "/files/Master/.", filepath.Join(stage, "Master")}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if output, e := adbCommand(ctx, bin, args...).CombinedOutput(); e != nil {
			return "", i18n.Wrap(fmt.Errorf("%s: %w", output, e), "supplement.error.pull")
		}
	} else {
		result, e := a.directResourceDirectories(serial, pkg, []string{"Master"}, stage)
		if e != nil {
			return "", e
		}
		if len(result.Errors) > 0 {
			return "", i18n.Wrap(fmt.Errorf("%v", result.Errors), "supplement.error.pull")
		}
	}
	version, err := supplement.Build(stage, root, decryptMasterBin)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	charts, err := supplement.UpdateCharts(ctx, root, &http.Client{Timeout: time.Minute})
	if err != nil {
		return i18n.T("supplement.chartsFailed", "version", version, "error", err.Error()), nil
	}
	return i18n.T("supplement.updatedDetail", "version", version, "charts", len(charts)), nil
}
