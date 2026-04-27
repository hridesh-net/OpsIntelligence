package security

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PolicyBundleFingerprint returns a stable sha256: digest of owner policy inputs
// under stateDir: POLICIES.md (if present) and all *.md files under teams/, sorted
// by path relative to stateDir. Empty string if nothing contributes.
func PolicyBundleFingerprint(stateDir string) string {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return ""
	}
	type fileEntry struct {
		rel  string
		data []byte
	}
	var files []fileEntry

	if b, err := os.ReadFile(filepath.Join(stateDir, "POLICIES.md")); err == nil && len(b) > 0 {
		files = append(files, fileEntry{rel: "POLICIES.md", data: b})
	}

	teamsDir := filepath.Join(stateDir, "teams")
	_ = filepath.WalkDir(teamsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(stateDir, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		files = append(files, fileEntry{rel: rel, data: b})
		return nil
	})

	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.rel))
		h.Write([]byte{0})
		h.Write(f.data)
		h.Write([]byte{0})
	}
	if len(files) == 0 {
		return ""
	}
	sum := h.Sum(nil)
	return "sha256:" + hex.EncodeToString(sum[:])
}
