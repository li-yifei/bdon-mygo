package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
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
