package views

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	staticfs "github.com/y0f/asura/web"
)

var (
	assetOnce   sync.Once
	assetHashes map[string]string
)

// Asset returns the URL of an embedded static file with a short content hash
// appended, so every stylesheet and script is cache-busted whenever its bytes
// change while unchanged files stay cached for the full max-age.
func Asset(basePath, name string) string {
	assetOnce.Do(loadAssetHashes)
	url := basePath + "/static/" + name
	if h, ok := assetHashes[name]; ok {
		return url + "?v=" + h
	}
	return url
}

func loadAssetHashes() {
	assetHashes = make(map[string]string)
	entries, err := staticfs.FS.ReadDir("static")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := staticfs.FS.ReadFile("static/" + e.Name())
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		assetHashes[e.Name()] = hex.EncodeToString(sum[:])[:12]
	}
}
