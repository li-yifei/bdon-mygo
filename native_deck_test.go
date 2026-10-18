package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unsafe"
)

func TestNativeDatasetIdentityBoundary(t *testing.T) {
	for _, id := range []string{"../private", "", "0123"} {
		if _, err := nativeDatasetPath(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	s := &nativeDeck{}
	sum := sha256.Sum256([]byte(`{}`))
	if _, err := s.Prepare(`{}`, hex.EncodeToString(sum[:])+"00", ""); err == nil {
		t.Fatal("accepted mismatching dataset")
	}
	if _, err := s.Recommend("missing", `{}`, `{}`); err == nil {
		t.Fatal("accepted unprepared dataset")
	}
}
func TestNativeAccountSolverLive(t *testing.T) {
	path := os.Getenv("OURNOTES_NATIVE_TEST_DATA")
	if path == "" {
		t.Skip("opt-in native dataset")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := loadRust(defaultFFILib())
	if err != nil {
		t.Fatal(err)
	}
	s := &nativeDeck{rust: r}
	sum := sha256.Sum256(b)
	ready, err := s.Prepare(string(b), hex.EncodeToString(sum[:]), "")
	if err != nil {
		t.Fatal(err)
	}
	answer, err := s.Recommend(ready.DatasetID, `{}`, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal([]byte(answer), &v); err != nil {
		t.Fatal(err)
	}
	if v["format"] != "ournotes-deck.account-recommendation/1" {
		t.Fatalf("unexpected answer format %v", v["format"])
	}
	if ready.CapabilitiesJSON == "" {
		t.Fatal("missing capabilities")
	}
}

// stubDeck answers datasetInfo with the given JSON, as the engine would.
func stubDeck(model, info string) *rustDeck {
	reply := func(text string) *byte { b := append([]byte(text), 0); return &b[0] }
	return &rustDeck{
		model:        model,
		datasetInfo:  func(*byte) *byte { return reply(info) },
		capabilities: func() *byte { return reply(`{}`) },
		stringLen: func(p *byte) uintptr {
			n := uintptr(0)
			for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
				n++
			}
			return n
		},
		free: func(*byte) {},
	}
}

func TestPrepareRefusesAnotherModelThanThePage(t *testing.T) {
	t.Setenv("MYGO_ENV", "development")
	t.Setenv("OURNOTES_DEV_STORAGE", t.TempDir())
	data := `{"modelCommit":"page"}`
	sum := sha256.Sum256([]byte(data))
	id := hex.EncodeToString(sum[:])
	info := `{"datasetId":"` + id + `","modelCommit":"page"}`

	s := &nativeDeck{rust: stubDeck("engine", info)}
	_, err := s.Prepare(data, id, "page")
	if err == nil || !strings.HasPrefix(err.Error(), errEngineModelMismatch) {
		t.Fatalf("prepare = %v, want %s", err, errEngineModelMismatch)
	}
	if m := s.modelMismatch(); m == nil || m.Engine != "engine" || m.Page != "page" {
		t.Fatalf("mismatch = %+v", m)
	}
	if _, err := s.Recommend(id, `{}`, `{}`); err == nil {
		t.Fatal("a refused dataset was registered")
	}
	// The same model, or an engine that declares none, prepares and clears it.
	for _, model := range []string{"page", ""} {
		s.setEngine(stubDeck(model, info))
		if _, err := s.Prepare(data, id, "page"); err != nil {
			t.Fatalf("model %q: %v", model, err)
		}
		if s.modelMismatch() != nil {
			t.Fatalf("model %q kept the mismatch", model)
		}
	}
}
