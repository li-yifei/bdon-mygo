package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAssetResolvesManifestDirectoryAndRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "packed", "cards"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"format":"ournotes.local/1","assetsDir":"packed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, "packed", "cards", "100.png")
	if err := os.WriteFile(asset, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := &resourceService{path: root}
	got := service.Asset("cards/100.png")
	if !got.Found || got.Path != asset || got.Relative != "cards/100.png" || got.URL == "" {
		t.Fatalf("unexpected asset ref: %+v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../manifest.json", "/etc/passwd", "missing.png"} {
		if ref := service.Asset(key); ref.Found {
			t.Fatalf("asset %q unexpectedly resolved: %+v", key, ref)
		}
	}
}
