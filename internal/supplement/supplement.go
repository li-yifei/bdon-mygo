// Package supplement fills in game content that the published bdon.moe data
// does not list yet: events, cards and songs decoded from the game Master on
// a device, and charts of new songs from bdon's chart site. The page merges
// it only where the published data lacks an entry, and drops it once the
// published data reaches the device's Master version.
package supplement

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/li-yifei/bdon-mygo/internal/i18n"
)

// schema.json lists the Master tables and columns the deck data uses.
//
//go:embed schema.json
var schemaJSON string

// Package is the only game package the supplement currently supports.
const Package = "com.bushiroad.sirius"

func dataPath(root string) string   { return filepath.Join(root, "event-overrides", "jp.json") }
func chartsPath(root string) string { return filepath.Join(root, "chart-overrides.json") }

// Data returns the stored supplement as JSON, or "null" when there is none.
func Data(root string) string {
	data, err := os.ReadFile(dataPath(root))
	if err == nil && len(data) <= 32<<20 && json.Valid(data) {
		return string(data)
	}
	return "null"
}

// Preload hands the stored supplement to the page script.
func Preload(root string) string {
	return "window.__ournotesEventPatch = " + Data(root) + ";\n"
}

// ReloadScript hands a freshly built supplement to the page and reloads it.
func ReloadScript(root string) string {
	return "sessionStorage.setItem('ournotes-event-refresh', JSON.stringify(" + Data(root) + ")); location.reload();"
}

// Decrypter decrypts one encrypted Master .bin file (header included).
type Decrypter func(data []byte) ([]byte, error)

// Build decodes a pulled Master directory (source/Master) into root's
// supplement, with the charts stored under root. It returns the Master version.
func Build(source, root string, decrypt Decrypter) (string, error) {
	var baseline struct {
		Master map[string][]string `json:"master"`
	}
	if err := json.Unmarshal([]byte(schemaJSON), &baseline); err != nil {
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
		Files   []struct {
			Name, Hash string
			Size       int64
		} `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(source, "Master", "MasterManifest.json"))
	if err != nil {
		return "", err
	}
	if err = json.Unmarshal(raw, &manifest); err != nil || manifest.Version == "" {
		return "", i18n.New("supplement.error.manifest")
	}
	files := map[string]struct {
		Hash string
		Size int64
	}{}
	for _, file := range manifest.Files {
		files[file.Name] = struct {
			Hash string
			Size int64
		}{file.Hash, file.Size}
	}
	tables := map[string][]map[string]any{}
	read := func(name string) error {
		info, ok := files[name+".bin"]
		if !ok {
			return i18n.New("supplement.error.missing", "name", name)
		}
		data, e := os.ReadFile(filepath.Join(source, "Master", name+".bin"))
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		if int64(len(data)) != info.Size || hex.EncodeToString(sum[:]) != info.Hash {
			return i18n.New("supplement.error.checksum", "name", name)
		}
		plain, e := decrypt(data)
		if e != nil {
			return fmt.Errorf("%s: %w", name, e)
		}
		gz, e := gzip.NewReader(bytes.NewReader(plain))
		if e != nil {
			return e
		}
		plain, e = io.ReadAll(io.LimitReader(gz, (64<<20)+1))
		gz.Close()
		if e != nil {
			return e
		}
		if len(plain) > 64<<20 {
			return i18n.New("supplement.error.tooLarge")
		}
		var table struct {
			Rows []map[string]any `json:"_allData"`
		}
		if e = json.Unmarshal(plain, &table); e != nil {
			return fmt.Errorf("%s: %w", name, e)
		}
		tables[name] = table.Rows
		return nil
	}
	for name := range baseline.Master {
		if err = read(name); err != nil {
			return "", err
		}
	}
	if err = read("MasterText"); err != nil {
		return "", err
	}
	master := map[string]any{}
	for name, cols := range baseline.Master {
		// Keep the published schema; new fields remain outside the solver's current contract.
		records := make([][]any, 0, len(tables[name]))
		for _, row := range tables[name] {
			values := make([]any, len(cols))
			for i, c := range cols {
				values[i] = row[c]
			}
			records = append(records, values)
		}
		master[name] = map[string]any{"columns": cols, "rows": records}
	}
	texts := map[string]string{}
	for _, r := range tables["MasterText"] {
		id, _ := r["_id"].(string)
		value, _ := r["_japanese"].(string)
		texts[id] = value
	}
	date := func(v any) string {
		s, _ := v.(string)
		if s == "" {
			return ""
		}
		return s + "+09:00"
	}
	events := []any{}
	for _, e := range tables["MasterEvent"] {
		music := []any{}
		rewards := []any{}
		for _, m := range tables["MasterChallengeMusic"] {
			if m["_eventId"] == e["_id"] {
				music = append(music, map[string]any{"id": m["_id"], "musicId": m["_liveMusicId"]})
			}
		}
		for _, name := range []string{"MasterLiveEventReward", "MasterChallengeLiveEventReward"} {
			challenge := name == "MasterChallengeLiveEventReward"
			group := e["_liveEventRewardGroup"]
			if challenge {
				group = e["_challengeLiveEventRewardGroup"]
			}
			for _, r := range tables[name] {
				if r["_resourceType"] == float64(1) && r["_resourceId"] == e["_eventItemId"] && r["_eventGroup"] == group {
					rewards = append(rewards, map[string]any{"id": r["_id"], "amount": r["_resourceCount"], "challenge": challenge})
				}
			}
		}
		name, _ := e["_nameTextId"].(string)
		events = append(events, map[string]any{"id": e["_id"], "name": texts[name], "startAt": date(e["_startAt"]), "endAt": date(e["_endAt"]), "itemId": e["_eventItemId"], "challengeMusics": music, "rewards": rewards})
	}
	if len(events) == 0 {
		return "", i18n.New("supplement.error.noEvents")
	}
	charts := json.RawMessage("[]")
	if data, e := os.ReadFile(chartsPath(root)); e == nil && json.Valid(data) {
		charts = data
	}
	payload := map[string]any{"format": "ournotes.event-supplement/2", "region": "jp", "version": manifest.Version, "generatedAt": time.Now().UTC().Format(time.RFC3339Nano), "events": events, "master": master, "texts": texts, "characters": tables["MasterCharacter"], "charts": charts}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	path := dataPath(root)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".supplement-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(encoded); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return manifest.Version, nil
}
