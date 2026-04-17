package localintel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultGGUFURL is the first URL tried for onboarding and `opsintelligence local-intel setup`
// when no explicit URL/env override is provided. Prefer the first-party GitHub release asset
// (attached by release CI — see .github/workflows/release.yml).
//
// Exposed as a var (not const) so tests can redirect the primary endpoint.
var DefaultGGUFURL = "https://github.com/hridesh-net/OpsIntelligence/releases/latest/download/gemma-4-e2b-it.gguf"

// FallbackGGUFURLs are tried when the GitHub asset is missing (old tags) or unreachable.
// Same Gemma 4 E2B-IT family, Q4_K_M quant on Hugging Face.
//
// Override with --url / OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL to use a single source only.
var FallbackGGUFURLs = []string{
	"https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF/resolve/main/gemma-4-E2B-it-Q4_K_M.gguf",
	"https://huggingface.co/bartowski/google_gemma-4-E2B-it-GGUF/resolve/main/google_gemma-4-E2B-it-Q4_K_M.gguf",
}

// defaultGGUFURLChain returns the ordered list of URLs we will try when the
// caller hasn't pinned one explicitly.
func defaultGGUFURLChain() []string {
	out := make([]string, 0, 1+len(FallbackGGUFURLs))
	out = append(out, DefaultGGUFURL)
	out = append(out, FallbackGGUFURLs...)
	return out
}

// DefaultGGUFPath returns the managed filesystem path used for downloaded GGUF weights.
func DefaultGGUFPath(stateDir string) string {
	return filepath.Join(stateDir, "models", "gemma-4-e2b-it.gguf")
}

// BootstrapOptions controls GGUF download/bootstrap behavior.
type BootstrapOptions struct {
	StateDir   string
	GGUFPath   string
	URL        string
	SHA256     string // optional lowercase/uppercase hex digest
	Force      bool
	Progress   io.Writer
	HTTPClient *http.Client
}

// BootstrapResult describes what happened during bootstrap.
type BootstrapResult struct {
	Path       string
	Downloaded bool
	Bytes      int64
}

// BootstrapGGUF ensures a GGUF exists on disk for local_intel. It reuses an existing file unless
// Force is set. When download is needed it fetches from URL (or env/default fallback) and optionally
// verifies SHA-256 when provided.
func BootstrapGGUF(ctx context.Context, opt BootstrapOptions) (BootstrapResult, error) {
	stateDir := strings.TrimSpace(opt.StateDir)
	if stateDir == "" {
		return BootstrapResult{}, fmt.Errorf("localintel bootstrap: state_dir is empty")
	}
	dst := strings.TrimSpace(opt.GGUFPath)
	if dst == "" {
		dst = DefaultGGUFPath(stateDir)
	}
	if !opt.Force {
		if st, err := os.Stat(dst); err == nil && !st.IsDir() {
			return BootstrapResult{Path: dst, Downloaded: false, Bytes: st.Size()}, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return BootstrapResult{}, fmt.Errorf("localintel bootstrap: mkdir for %q: %w", dst, err)
	}

	// Build the ordered list of URLs to try. An explicit opt.URL or the
	// OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL env var pins a single source
	// and disables the fallback chain. Otherwise: GitHub release asset first,
	// then public HF mirrors.
	var urls []string
	if u := strings.TrimSpace(opt.URL); u != "" {
		urls = []string{u}
	} else if u := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL")); u != "" {
		urls = []string{u}
	} else {
		urls = defaultGGUFURLChain()
	}

	sha := strings.ToLower(strings.TrimSpace(opt.SHA256))
	if sha == "" {
		sha = strings.ToLower(strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_SHA256")))
	}

	client := opt.HTTPClient
	if client == nil {
		client = &http.Client{}
	}

	var (
		lastErr error
		n       int64
		used    string
	)
	for i, url := range urls {
		attempt, err := downloadGGUF(ctx, client, url, dst, sha, opt.Progress)
		if err == nil {
			n = attempt
			used = url
			break
		}
		lastErr = err
		// Only retry with the next URL if this one looks like a missing
		// asset (transport error or non-2xx). SHA-256 mismatch on a
		// byte-for-byte pinned download is a real corruption/attack
		// signal and MUST NOT be silently swallowed by trying another
		// mirror.
		if errors.Is(err, errSHAMismatch) {
			return BootstrapResult{}, err
		}
		if i < len(urls)-1 && opt.Progress != nil {
			fmt.Fprintf(opt.Progress, "localintel bootstrap: %v — trying next mirror\n", err)
		}
	}
	if used == "" {
		return BootstrapResult{}, fmt.Errorf("localintel bootstrap: all sources failed (last error: %w)", lastErr)
	}
	return BootstrapResult{Path: dst, Downloaded: true, Bytes: n}, nil
}

// errSHAMismatch flags integrity-check failures so the URL chain doesn't
// mask them by silently moving on to the next mirror.
var errSHAMismatch = errors.New("localintel bootstrap: sha256 mismatch")

// downloadGGUF fetches a single URL into dst (via a temp file), enforcing
// sha (when non-empty) and emitting progress to progressW (when non-nil).
// On any non-integrity failure it cleans up the temp file so the caller
// can retry the next URL in the chain.
func downloadGGUF(ctx context.Context, client *http.Client, url, dst, sha string, progressW io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("localintel bootstrap: request: %w", err)
	}
	if tok := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("localintel bootstrap: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("localintel bootstrap: GET %s: status %s", url, resp.Status)
	}

	tmp := dst + ".download"
	out, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("localintel bootstrap: create temp: %w", err)
	}

	hasher := sha256.New()
	src := io.TeeReader(resp.Body, hasher)
	var progress *downloadProgress
	if progressW != nil {
		progress = newDownloadProgress(progressW, resp.ContentLength)
		src = io.TeeReader(src, progress)
	}
	n, copyErr := io.Copy(out, src)
	if progress != nil {
		progress.finish()
	}
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("localintel bootstrap: download/write: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("localintel bootstrap: close temp: %w", closeErr)
	}

	gotSHA := strings.ToLower(hex.EncodeToString(hasher.Sum(nil)))
	if sha != "" && gotSHA != sha {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("%w: got %s want %s", errSHAMismatch, gotSHA, sha)
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("localintel bootstrap: move into place: %w", err)
	}
	return n, nil
}

type downloadProgress struct {
	w           io.Writer
	total       int64
	downloaded  int64
	startedAt   time.Time
	lastPrinted time.Time
	lastWidth   int
}

func newDownloadProgress(w io.Writer, total int64) *downloadProgress {
	now := time.Now()
	return &downloadProgress{
		w:           w,
		total:       total,
		startedAt:   now,
		lastPrinted: now,
	}
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	n := len(b)
	p.downloaded += int64(n)
	now := time.Now()
	if now.Sub(p.lastPrinted) >= 500*time.Millisecond {
		p.print(false)
		p.lastPrinted = now
	}
	return n, nil
}

func (p *downloadProgress) finish() {
	p.print(true)
}

func (p *downloadProgress) print(done bool) {
	secs := time.Since(p.startedAt).Seconds()
	speed := 0.0
	if secs > 0 {
		speed = float64(p.downloaded) / (1024 * 1024) / secs
	}
	if p.total > 0 {
		pct := (float64(p.downloaded) / float64(p.total)) * 100
		if pct > 100 {
			pct = 100
		}
		line := fmt.Sprintf(
			"GGUF [%s] %3.0f%% %.0f/%.0fMB %.1fMB/s",
			renderProgressBar(pct, 16),
			pct,
			float64(p.downloaded)/(1024*1024),
			float64(p.total)/(1024*1024),
			speed,
		)
		p.writeLine(line)
	} else {
		line := fmt.Sprintf(
			"GGUF %.0fMB %.1fMB/s",
			float64(p.downloaded)/(1024*1024),
			speed,
		)
		p.writeLine(line)
	}
	if done {
		_, _ = fmt.Fprint(p.w, "\n")
	}
}

func (p *downloadProgress) writeLine(line string) {
	padding := ""
	if extra := p.lastWidth - len(line); extra > 0 {
		padding = strings.Repeat(" ", extra)
	}
	_, _ = fmt.Fprintf(p.w, "\r%s%s", line, padding)
	p.lastWidth = len(line)
}

func renderProgressBar(percent float64, width int) string {
	if width <= 0 {
		width = 20
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int((percent / 100.0) * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
}
