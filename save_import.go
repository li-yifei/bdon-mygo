package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// readLocalPlayerSave returns the exact _player JSON bytes, preserving integer IDs.
// New ADB saves take precedence; an older decoded cache never replaces a newer raw save.
func readLocalPlayerSave(root string) (playerText, accountID, server string, err error) {
	type candidate struct {
		path     string
		modified time.Time
		server   string
	}
	var candidates []candidate
	adbRoot := filepath.Join(root, "android", "adb")
	entries, _ := os.ReadDir(adbRoot)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != "" {
			continue
		}
		info, e := entry.Info()
		if e != nil || info.Size() < 64 || info.Size() > 16<<20 {
			continue
		}
		p := filepath.Join(adbRoot, entry.Name())
		candidates = append(candidates, candidate{p, info.ModTime(), saveServer(p + ".json")})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].modified.After(candidates[j].modified) })
	key, magic, keyErr := localSaveCredentials()
	var decryptErr error
	for _, c := range candidates {
		data, e := os.ReadFile(c.path)
		if e != nil {
			decryptErr = e
			continue
		}
		if json.Valid(data) {
			playerText, accountID, e = extractLocalPlayer(data)
		} else if keyErr == nil && len(data) >= 64 && bytes.Equal(data[:32], magic[:]) {
			var iv [32]byte
			copy(iv[:], data[32:64])
			plain, de := newOurNotesRijndael256(key).decrypt(data[64:], iv)
			if de != nil {
				decryptErr = de
				continue
			}
			playerText, accountID, e = extractLocalPlayer(plain)
		} else {
			continue
		}
		if e == nil {
			if c.server == "" {
				c.server = saveServer(filepath.Join(adbRoot, "device.json"))
			}
			if c.server == "" {
				return "", "", "", i18n.New("save.error.noServerMetadata")
			}
			return playerText, accountID, c.server, nil
		}
		decryptErr = e
	}
	fallback := filepath.Join(root, "android", "account", "player.json")
	info, e := os.Stat(fallback)
	if e != nil {
		// Encrypted saves were pulled but none could be opened: name the real cause
		// instead of the missing legacy plaintext cache.
		if len(candidates) > 0 && keyErr != nil {
			return "", "", "", i18n.Wrap(keyErr, "save.error.needCredentials", "path", saveCredentialsFile())
		}
		if len(candidates) > 0 && decryptErr != nil {
			return "", "", "", i18n.Wrap(decryptErr, "save.error.decrypt")
		}
		if len(candidates) == 0 {
			return "", "", "", i18n.Wrap(e, "save.error.notPulled")
		}
		return "", "", "", i18n.Wrap(e, "save.error.read")
	}
	if len(candidates) > 0 && candidates[0].modified.After(info.ModTime()) {
		if keyErr != nil {
			decryptErr = keyErr
		}
		return "", "", "", i18n.Wrap(decryptErr, "save.error.staleCache", "time", candidates[0].modified.Format(time.RFC3339))
	}
	data, e := os.ReadFile(fallback)
	if e != nil {
		return "", "", "", e
	}
	playerText, accountID, e = extractLocalPlayer(data)
	if e != nil {
		return "", "", "", e
	}
	server = saveServer(fallback + ".meta.json")
	if server == "" && len(candidates) > 0 {
		server = candidates[0].server
	}
	if server == "" {
		server = saveServer(filepath.Join(adbRoot, "device.json"))
	}
	if server == "" {
		return "", "", "", i18n.New("save.error.noServer")
	}
	return playerText, accountID, server, nil
}

func saveServer(path string) string {
	data, e := os.ReadFile(path)
	if e != nil {
		return ""
	}
	var meta struct {
		Package string `json:"package"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	switch meta.Package {
	case "com.bushiroad.sirius":
		return "jp"
	case "com.bilibili.sirius", "com.bilibili.sirius.official":
		return "intl"
	}
	return ""
}

func extractLocalPlayer(data []byte) (string, string, error) {
	var object map[string]json.RawMessage
	if e := json.Unmarshal(data, &object); e != nil {
		return "", "", e
	}
	player := json.RawMessage(data)
	if wrapped, ok := object["_player"]; ok {
		player = wrapped
		if e := json.Unmarshal(player, &object); e != nil {
			return "", "", e
		}
	}
	id, ok := object["_profileId"]
	if !ok {
		return "", "", i18n.New("save.error.noProfileID")
	}
	var accountID string
	if e := json.Unmarshal(id, &accountID); e != nil {
		text := string(bytes.TrimSpace(id))
		if text == "" {
			return "", "", i18n.New("save.error.emptyPlayerID")
		}
		for _, r := range text {
			if r < '0' || r > '9' {
				return "", "", i18n.New("save.error.badPlayerID")
			}
		}
		accountID = text
	}
	if accountID == "" {
		return "", "", i18n.New("save.error.emptyPlayerID")
	}
	return string(player), accountID, nil
}

// maskedSecret decodes a value written by scripts/embed-save-credentials.py.
func maskedSecret(h string) (out [32]byte) {
	for i := range out {
		b, err := hex.DecodeString(h[i*2 : i*2+2])
		if err != nil {
			return [32]byte{}
		}
		out[i] = b[0] ^ byte((i*29+71)&255)
	}
	return out
}

// saveCredentialsFile is the save.env this run reads: OURNOTES_SAVE_CREDENTIALS,
// else the platform location of saveCredentialsPath.
func saveCredentialsFile() string {
	if path := os.Getenv("OURNOTES_SAVE_CREDENTIALS"); path != "" {
		return path
	}
	home, _ := os.UserHomeDir()
	return saveCredentialsPath(runtime.GOOS, home, currentAppDirs().Config)
}

func localSaveCredentials() (key, magic [32]byte, err error) {
	values := map[string]string{"OURNOTES_SAVE_KEY": os.Getenv("OURNOTES_SAVE_KEY"), "OURNOTES_SAVE_MAGIC": os.Getenv("OURNOTES_SAVE_MAGIC")}
	path := saveCredentialsFile()
	if info, e := os.Stat(path); e == nil {
		// Windows guards files with ACLs, not mode bits: Go reports 0666 for
		// every writable file there, and %AppData% is private to the user.
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return key, magic, i18n.New("save.error.credentialsMode")
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return key, magic, e
		}
		for _, line := range strings.Split(string(data), "\n") {
			name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && values[name] == "" {
				values[name] = strings.Trim(strings.TrimSpace(value), "\"'")
			}
		}
	}
	for name, dest := range map[string]*[32]byte{"OURNOTES_SAVE_KEY": &key, "OURNOTES_SAVE_MAGIC": &magic} {
		decoded, e := hex.DecodeString(values[name])
		if e != nil || len(decoded) != 32 {
			if extensionSaveCredentials != nil {
				if k, m, ok := extensionSaveCredentials(); ok {
					return k, m, nil
				}
			}
			return key, magic, i18n.New("save.error.noCredentials")
		}
		copy(dest[:], decoded)
	}
	return key, magic, nil
}

// saveCredentialsPath returns where save.env is read:
//
//	macOS:   ~/.config/<credentialsDir>/save.env, else the file of the
//	         previous product name (TODO: drop the fallback after one release)
//	Linux:   the config directory, $XDG_CONFIG_HOME/<dir>/save.env (~/.config)
//	Windows: the config directory, %APPDATA%\<dir>\save.env
//
// (in portable mode <exe dir>/data/config/save.env). appConfig is the app's
// config directory; home is the user's home directory.
func saveCredentialsPath(goos, home, appConfig string) string {
	if goos != "darwin" {
		if appConfig == "" {
			appConfig = filepath.Join(home, ".config", product.CredentialsDir)
		}
		return filepath.Join(appConfig, "save.env")
	}
	path := filepath.Join(home, ".config", product.CredentialsDir, "save.env")
	if _, err := os.Stat(path); err != nil {
		legacy := filepath.Join(home, ".config", product.LegacyCredentialsDir, "save.env")
		if _, e := os.Stat(legacy); e == nil {
			return legacy
		}
	}
	return path
}
