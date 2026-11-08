package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Check the actual embedded bytes, so an asset update cannot silently leave its
// provenance manifest or redistribution notices behind.
func TestVendoredAssets(t *testing.T) {
	data, err := os.ReadFile("vendor.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Notices   string `json:"notices"`
		Libraries []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			License string `json:"license"`
			Files   []struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	notices, err := FS.ReadFile(manifest.Notices)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Libraries) == 0 {
		t.Fatal("empty vendor manifest")
	}
	seen := make(map[string]bool)
	for _, library := range manifest.Libraries {
		t.Run(library.Name, func(t *testing.T) {
			if library.Version == "" || library.License == "" || len(library.Files) == 0 {
				t.Fatal("incomplete library metadata")
			}
			if !strings.Contains(string(notices), library.Name+" "+library.Version) {
				t.Error("missing redistribution notice")
			}
			for _, file := range library.Files {
				if seen[file.Path] {
					t.Errorf("duplicate asset %s", file.Path)
				}
				seen[file.Path] = true
				content, err := FS.ReadFile(file.Path)
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(content)
				if hex.EncodeToString(digest[:]) != file.SHA256 {
					t.Errorf("asset %s differs from its recorded SHA-256", file.Path)
				}
			}
		})
	}
}
