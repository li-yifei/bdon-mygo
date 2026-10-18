package supplement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Charts of songs that the published deck data lacks are read from bdon's
// chart site, which often lists a song from another server before the
// Japanese data has it. A chart is kept only while the published deck data
// has no chart of its score ID.
const (
	chartSite = "https://assets.bdon.moe/chart-site/"
	musicData = "https://storage.bdon.moe/moenotes/jp/music-data/"
)

var difficulties = []string{"easy", "normal", "hard", "expert"}

// judgementTypes maps the chart site's judgement names to the deck data's
// codes, as the published charts pair them.
var judgementTypes = map[string]int{
	"Normal": 1, "EasyNormal": 2, "Flick": 5, "SlideBegin": 10, "SlideEnd": 11,
	"SlideBeginEasy": 15, "Trace": 21, "SlideEndTrace": 22,
}

// Chart is one chart in the deck data's format.
type Chart struct {
	ScoreID int `json:"scoreId"`
	Asset   struct {
		Key    string `json:"key"`
		SHA256 string `json:"sha256"`
	} `json:"asset"`
	Notes struct {
		ID            []int `json:"id"`
		Op            []int `json:"op"`
		JudgementType []int `json:"judgementType"`
		TimeMs        []int `json:"timeMs"`
	} `json:"notes"`
	SkillEvents struct {
		TimeMs []int `json:"timeMs"`
	} `json:"skillEvents"`
	Fevers struct {
		StartMs []int `json:"startMs"`
		EndMs   []int `json:"endMs"`
	} `json:"fevers"`
}

type liveNote struct {
	ID            int      `json:"id"`
	Op            int      `json:"op"`
	JudgementType string   `json:"judgementType"`
	TimeMs        int      `json:"timeMs"`
	Tick          *float64 `json:"tick"`
}

type liveLine struct {
	NoteIDs  []int `json:"noteIds"`
	ComboIDs []int `json:"comboIds"`
}

type timePoint struct {
	TimeMs int `json:"timeMs"`
}

// liveScore is the part of the chart site's nnnotes.live-score/1 a chart needs.
type liveScore struct {
	Notes []liveNote  `json:"notes"`
	Lines []liveLine  `json:"lines"`
	Skill []timePoint `json:"skill"`
	Fever []struct {
		Start timePoint `json:"start"`
		End   timePoint `json:"end"`
	} `json:"fever"`
	Source struct {
		Key string `json:"key"`
	} `json:"source"`
}

// gameOrder lists notes in the client's order, as the published charts list
// them. Chart notes go in id order, grouped by chart tick. A combo tick within
// 1 ms of a group follows the nearest such group (exact matches first, then
// the later group) unless its line ends there. After a group, the pending
// ticks of the lines that end in it go line by line (last note, then first
// note), each by time and followed by the other pending ticks at exactly its
// time; ticks left at the end follow by time.
func gameOrder(s liveScore) ([]liveNote, error) {
	byID := make(map[int]liveNote, len(s.Notes))
	for _, n := range s.Notes {
		if _, dup := byID[n.ID]; dup {
			return nil, fmt.Errorf("note %d listed twice", n.ID)
		}
		byID[n.ID] = n
	}
	type lineKey struct{ end, begin int }
	lineOf := map[int]int{} // combo tick -> line index
	keyOf := map[int]lineKey{}
	endsAt := map[int][]int{} // chart note -> lines ending there
	for i, l := range s.Lines {
		if len(l.NoteIDs) == 0 {
			continue
		}
		endsAt[l.NoteIDs[len(l.NoteIDs)-1]] = append(endsAt[l.NoteIDs[len(l.NoteIDs)-1]], i)
		for _, c := range l.ComboIDs {
			if _, ok := byID[c]; !ok {
				return nil, fmt.Errorf("line tick %d is not a note", c)
			}
			lineOf[c] = i
			keyOf[c] = lineKey{l.NoteIDs[len(l.NoteIDs)-1], l.NoteIDs[0]}
		}
	}
	less := func(a, b lineKey) bool { return a.end < b.end || a.end == b.end && a.begin < b.begin }
	var base []liveNote
	for _, n := range s.Notes {
		if _, tick := lineOf[n.ID]; !tick {
			base = append(base, n)
		}
	}
	sort.Slice(base, func(i, j int) bool { return base[i].ID < base[j].ID })
	groupKey := func(n liveNote) float64 {
		if n.Tick != nil {
			return *n.Tick
		}
		return float64(n.TimeMs)
	}
	var groups [][]liveNote
	for i := 0; i < len(base); {
		j := i
		for j < len(base) && groupKey(base[j]) == groupKey(base[i]) {
			j++
		}
		groups = append(groups, base[i:j])
		i = j
	}
	near := map[int][2]int{} // tick -> {distance, group}
	for g, group := range groups {
		t := group[0].TimeMs
		for c := range lineOf {
			d := int(math.Abs(float64(byID[c].TimeMs - t)))
			if old, ok := near[c]; d <= 1 && (!ok || d <= old[0]) {
				near[c] = [2]int{d, g}
			}
		}
	}
	pending := map[int]bool{}
	for c := range lineOf {
		pending[c] = true
	}
	order := make([]liveNote, 0, len(s.Notes))
	emit := func(ids []int) {
		for _, c := range ids {
			if pending[c] {
				delete(pending, c)
				order = append(order, byID[c])
			}
		}
	}
	sorted := func(ids []int, key func(a, b int) bool) []int {
		sort.Slice(ids, func(i, j int) bool { return key(ids[i], ids[j]) })
		return ids
	}
	byLine := func(a, b int) bool {
		if keyOf[a] != keyOf[b] {
			return less(keyOf[a], keyOf[b])
		}
		return a < b
	}
	at := func(t int) []int {
		var ids []int
		for c := range pending {
			if byID[c].TimeMs == t {
				ids = append(ids, c)
			}
		}
		return sorted(ids, byLine)
	}
	for g, group := range groups {
		order = append(order, group...)
		ended := map[int]bool{}
		for _, n := range group {
			for _, l := range endsAt[n.ID] {
				ended[l] = true
			}
		}
		var paired []int
		for c := range pending {
			if v, ok := near[c]; ok && v[1] == g && !ended[lineOf[c]] {
				paired = append(paired, c)
			}
		}
		emit(sorted(paired, byLine))
		var own []int
		for l := range ended {
			for _, c := range s.Lines[l].ComboIDs {
				if pending[c] {
					own = append(own, c)
				}
			}
		}
		own = sorted(own, func(a, b int) bool {
			if keyOf[a] != keyOf[b] {
				return less(keyOf[a], keyOf[b])
			}
			if byID[a].TimeMs != byID[b].TimeMs {
				return byID[a].TimeMs < byID[b].TimeMs
			}
			return a < b
		})
		for _, c := range own {
			if pending[c] {
				t := byID[c].TimeMs
				emit([]int{c})
				emit(at(t))
			}
		}
	}
	var rest []int
	for c := range pending {
		rest = append(rest, c)
	}
	emit(sorted(rest, func(a, b int) bool {
		if byID[a].TimeMs != byID[b].TimeMs {
			return byID[a].TimeMs < byID[b].TimeMs
		}
		return byLine(a, b)
	}))
	return order, nil
}

// chartOf converts a live score into the deck data's chart format.
func chartOf(scoreID int, s liveScore) (Chart, error) {
	var c Chart
	notes, err := gameOrder(s)
	if err != nil {
		return c, err
	}
	c.ScoreID = scoreID
	c.Asset.Key = s.Source.Key
	for _, n := range notes {
		code, ok := judgementTypes[n.JudgementType]
		if !ok {
			return c, fmt.Errorf("unknown judgement type %q", n.JudgementType)
		}
		c.Notes.ID = append(c.Notes.ID, n.ID)
		c.Notes.Op = append(c.Notes.Op, n.Op)
		c.Notes.JudgementType = append(c.Notes.JudgementType, code)
		c.Notes.TimeMs = append(c.Notes.TimeMs, n.TimeMs)
	}
	for _, e := range s.Skill {
		c.SkillEvents.TimeMs = append(c.SkillEvents.TimeMs, e.TimeMs)
	}
	for _, f := range s.Fever {
		c.Fevers.StartMs = append(c.Fevers.StartMs, f.Start.TimeMs)
		c.Fevers.EndMs = append(c.Fevers.EndMs, f.End.TimeMs)
	}
	if len(c.Notes.ID) == 0 || len(c.SkillEvents.TimeMs) != 5 || len(c.Fevers.StartMs) != 3 {
		return c, errors.New("incomplete chart")
	}
	return c, nil
}

var errNotFound = errors.New("not found")

func getJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(v)
}

// fetchLiveScore reads one chart's live score from the chart site, whole or
// split into one asset per top-level field.
func fetchLiveScore(ctx context.Context, client *http.Client, musicID int, difficulty string) (liveScore, error) {
	var s liveScore
	var page struct {
		Files map[string]struct {
			Asset string              `json:"asset"`
			Parts [][]json.RawMessage `json:"parts"`
		} `json:"files"`
	}
	if err := getJSON(ctx, client, fmt.Sprintf("%scharts/%d_%s.json", chartSite, musicID, difficulty), &page); err != nil {
		return s, err
	}
	for name, file := range page.Files {
		if !strings.HasPrefix(name, "score/") || !strings.HasSuffix(name, ".notes.json") {
			continue
		}
		if file.Asset != "" {
			return s, getJSON(ctx, client, chartSite+file.Asset, &s)
		}
		whole := map[string]json.RawMessage{}
		for _, part := range file.Parts {
			var key, asset string
			if len(part) < 2 || json.Unmarshal(part[0], &key) != nil || json.Unmarshal(part[1], &asset) != nil {
				return s, errors.New("malformed chart parts")
			}
			switch key {
			case "notes", "lines", "skill", "fever", "source":
				var value json.RawMessage
				if err := getJSON(ctx, client, chartSite+asset, &value); err != nil {
					return s, err
				}
				whole[key] = value
			}
		}
		data, _ := json.Marshal(whole)
		return s, json.Unmarshal(data, &s)
	}
	return s, errNotFound
}

// publishedScoreIDs lists the score IDs of the published Japanese deck data.
func publishedScoreIDs(ctx context.Context, client *http.Client) (map[int]bool, error) {
	var build struct {
		Replay struct {
			ManifestURL string `json:"manifestUrl"`
		} `json:"replay"`
	}
	if err := getJSON(ctx, client, musicData+"build.json", &build); err != nil {
		return nil, err
	}
	var manifest struct {
		Charts []struct {
			ScoreID int `json:"scoreId"`
		} `json:"charts"`
	}
	if build.Replay.ManifestURL == "" {
		return nil, errors.New("published data names no manifest")
	}
	if err := getJSON(ctx, client, musicData+build.Replay.ManifestURL, &manifest); err != nil {
		return nil, err
	}
	ids := make(map[int]bool, len(manifest.Charts))
	for _, c := range manifest.Charts {
		ids[c.ScoreID] = true
	}
	return ids, nil
}

// UpdateCharts reads from the chart site the charts of the stored
// supplement's songs that the published deck data lacks, keeps them under
// root and puts them in the supplement. Charts the published data now has
// are dropped. It returns the score IDs of the charts kept.
func UpdateCharts(ctx context.Context, root string, client *http.Client) ([]int, error) {
	raw, err := os.ReadFile(dataPath(root))
	if err != nil {
		return nil, err
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	var master map[string]struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err = json.Unmarshal(payload["master"], &master); err != nil {
		return nil, err
	}
	published, err := publishedScoreIDs(ctx, client)
	if err != nil {
		return nil, err
	}
	scores := map[int]bool{}
	if t, ok := master["MasterLiveMusicScore"]; ok {
		for _, r := range t.Rows {
			for i, c := range t.Columns {
				if c == "_id" {
					if v, ok := r[i].(float64); ok {
						scores[int(v)] = true
					}
				}
			}
		}
	}
	kept := map[int]Chart{}
	var stored []Chart
	if data, e := os.ReadFile(chartsPath(root)); e == nil {
		_ = json.Unmarshal(data, &stored)
	}
	for _, c := range stored {
		if !published[c.ScoreID] {
			kept[c.ScoreID] = c
		}
	}
	if t, ok := master["MasterLiveMusic"]; ok {
		col := map[string]int{}
		for i, c := range t.Columns {
			col[c] = i
		}
		for _, r := range t.Rows {
			id, _ := r[col["_id"]].(float64)
			for _, d := range difficulties {
				k, ok := col["_"+d+"ID"]
				if !ok {
					continue
				}
				scoreID, _ := r[k].(float64)
				sid := int(scoreID)
				if sid == 0 || published[sid] || !scores[sid] {
					continue
				}
				if _, have := kept[sid]; have {
					continue
				}
				s, e := fetchLiveScore(ctx, client, int(id), d)
				if errors.Is(e, errNotFound) {
					continue // The chart site does not list this chart yet.
				}
				if e != nil {
					return nil, fmt.Errorf("chart %d: %w", sid, e)
				}
				c, e := chartOf(sid, s)
				if e != nil {
					return nil, fmt.Errorf("chart %d: %w", sid, e)
				}
				kept[sid] = c
			}
		}
	}
	charts := make([]Chart, 0, len(kept))
	for _, c := range kept {
		charts = append(charts, c)
	}
	sort.Slice(charts, func(i, j int) bool { return charts[i].ScoreID < charts[j].ScoreID })
	encoded, err := json.Marshal(charts)
	if err != nil {
		return nil, err
	}
	if err = writeFile(chartsPath(root), encoded); err != nil {
		return nil, err
	}
	payload["charts"] = encoded
	if raw, err = json.Marshal(payload); err != nil {
		return nil, err
	}
	if err = writeFile(dataPath(root), raw); err != nil {
		return nil, err
	}
	ids := make([]int, len(charts))
	for i, c := range charts {
		ids[i] = c.ScoreID
	}
	return ids, nil
}

// writeFile replaces path atomically.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".supplement-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
