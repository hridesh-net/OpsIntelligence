package prompts

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader discovers prompts and chains on the filesystem. A source layout
// looks like:
//
//	<dir>/
//	  pr-review/
//	    gather.md
//	    analyze.md
//	    critique.md
//	    render.md
//	  meta/
//	    self-critique.md
//	    evidence-extractor.md
//	  chains/
//	    pr-review.yaml
//	    sonar-triage.yaml
//
// Every *.md file under <dir> (excluding chains/) is a SmartPrompt.
// Every *.yaml file under <dir>/chains/ is a Chain.
//
// When `embedded` is non-nil it is consulted first, then the disk layout
// at `dir` is overlaid (disk wins per-ID). This lets the binary ship
// sensible defaults while letting operators customise anything on disk
// under ~/.opsintelligence/prompts/.
type Loader struct {
	// Embedded is an optional read-only filesystem (typically a
	// go:embed FS) scanned before Dir. Tests can pass fs.FS fixtures.
	Embedded fs.FS
	// EmbeddedRoot is the subpath within Embedded to scan. Ignored when
	// Embedded is nil.
	EmbeddedRoot string
	// Dir is the on-disk directory that overrides embedded defaults.
	// Missing directories are not an error.
	Dir string
}

// Load walks both Embedded and Dir, merging results into a fresh Library.
// Disk entries win by ID over embedded ones.
func (ld Loader) Load() (*Library, error) {
	lib := NewLibrary()
	if ld.Embedded != nil && ld.EmbeddedRoot != "" {
		if err := ld.loadFromFS(lib, ld.Embedded, ld.EmbeddedRoot, "<embedded>"); err != nil {
			return nil, fmt.Errorf("prompts: load embedded: %w", err)
		}
	}
	if ld.Dir != "" {
		if st, err := os.Stat(ld.Dir); err == nil && st.IsDir() {
			if err := ld.loadFromFS(lib, os.DirFS(ld.Dir), ".", ld.Dir); err != nil {
				return nil, fmt.Errorf("prompts: load %s: %w", ld.Dir, err)
			}
		}
	}
	return lib, nil
}

func (ld Loader) loadFromFS(lib *Library, fsys fs.FS, root, display string) error {
	chainPaths := []string{}
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(path)
		switch {
		case strings.HasSuffix(lower, ".md"):
			if strings.Contains(path, "/chains/") || strings.HasPrefix(path, "chains/") {
				return nil
			}
			return ld.loadPromptFile(lib, fsys, path, display)
		case strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml"):
			if strings.Contains(path, "/chains/") || strings.HasPrefix(path, "chains/") {
				chainPaths = append(chainPaths, path)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range chainPaths {
		if err := ld.loadChainFile(lib, fsys, p, display); err != nil {
			return err
		}
	}
	return nil
}

func (ld Loader) loadPromptFile(lib *Library, fsys fs.FS, path, display string) error {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	meta, body, err := splitFrontmatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var sp SmartPrompt
	if len(meta) > 0 {
		if err := yaml.Unmarshal(meta, &sp); err != nil {
			return fmt.Errorf("%s: frontmatter: %w", path, err)
		}
	}
	if sp.ID == "" {
		// Derive an ID from the directory + basename when unset.
		sp.ID = deriveIDFromPath(path)
	}
	if sp.Name == "" {
		sp.Name = sp.ID
	}
	sp.Body = strings.TrimSpace(string(body))
	if display == "<embedded>" {
		sp.SourcePath = "embedded:" + path
	} else {
		sp.SourcePath = filepath.Join(display, path)
	}
	return lib.AddPrompt(&sp)
}

func (ld Loader) loadChainFile(lib *Library, fsys fs.FS, path, display string) error {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var c Chain
	if err := yaml.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if c.ID == "" {
		c.ID = deriveIDFromPath(path)
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	if display == "<embedded>" {
		c.SourcePath = "embedded:" + path
	} else {
		c.SourcePath = filepath.Join(display, path)
	}
	return lib.AddChain(&c)
}

// splitFrontmatter separates a YAML frontmatter block (between two lines
// containing only `---`) from the Markdown body. Files without
// frontmatter return empty meta and the full body.
func splitFrontmatter(data []byte) (meta, body []byte, err error) {
	trim := bytes.TrimLeft(data, " \t\r\n")
	if !bytes.HasPrefix(trim, []byte("---")) {
		return nil, data, nil
	}
	rest := bytes.TrimPrefix(trim, []byte("---"))
	rest = bytes.TrimLeft(rest, " \t\r\n")
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return nil, nil, fmt.Errorf("unterminated frontmatter")
	}
	meta = rest[:end]
	body = rest[end+len("\n---"):]
	body = bytes.TrimLeft(body, " \t\r\n")
	return meta, body, nil
}

// deriveIDFromPath turns "pr-review/gather.md" into "pr-review/gather"
// and "chains/pr-review.yaml" into "pr-review".
func deriveIDFromPath(path string) string {
	p := strings.TrimSuffix(path, filepath.Ext(path))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "chains/")
	return p
}
