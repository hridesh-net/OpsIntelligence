package localintel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultGGUFPath(t *testing.T) {
	got := DefaultGGUFPath("/tmp/ac")
	want := filepath.Join("/tmp/ac", "models", "gemma-4-e2b-it.gguf")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBootstrapGGUF_DownloadAndReuse(t *testing.T) {
	payload := []byte("gguf-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	sd := t.TempDir()
	res, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: sd, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Downloaded {
		t.Fatalf("expected Downloaded=true, got %+v", res)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(payload) {
		t.Fatalf("payload mismatch: %q", string(b))
	}

	res2, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: sd, URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Downloaded {
		t.Fatalf("expected reuse without download, got %+v", res2)
	}
}

func TestBootstrapGGUF_SHA256Mismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()
	_, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: t.TempDir(), URL: srv.URL, SHA256: "deadbeef"})
	if err == nil {
		t.Fatal("expected sha mismatch")
	}
}

func TestBootstrapGGUF_SHA256OK(t *testing.T) {
	payload := []byte("ok")
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	_, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: t.TempDir(), URL: srv.URL, SHA256: want})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapGGUF_BearerToken(t *testing.T) {
	t.Setenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_TOKEN", "abc123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer abc123" {
			t.Fatalf("auth header: got %q", got)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	_, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: t.TempDir(), URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
}

// When the first URL 404s and a later URL succeeds, BootstrapGGUF should
// transparently fall through the chain (e.g. primary HF mirror down,
// second mirror OK).
func TestBootstrapGGUF_FallbackURL_SkipsFailedPrimary(t *testing.T) {
	payload := []byte("fallback-gguf-bytes")
	hits := make(map[string]int)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits["bad"]++
		http.NotFound(w, r)
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits["good"]++
		_, _ = w.Write(payload)
	}))
	defer good.Close()

	origDefault := DefaultGGUFURL_forTest_setValue(bad.URL)
	origFallbacks := FallbackGGUFURLs
	FallbackGGUFURLs = []string{good.URL}
	t.Cleanup(func() {
		DefaultGGUFURL_forTest_setValue(origDefault)
		FallbackGGUFURLs = origFallbacks
	})

	res, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	if !res.Downloaded {
		t.Fatalf("expected Downloaded=true, got %+v", res)
	}
	if hits["bad"] != 1 || hits["good"] != 1 {
		t.Fatalf("expected exactly one hit on each server, got %+v", hits)
	}
	b, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(payload) {
		t.Fatalf("payload mismatch, got %q", string(b))
	}
}

// When the caller pins an explicit URL (--url / env), the fallback chain
// must NOT be consulted — failing loudly is the right signal that the
// pinned source is broken.
func TestBootstrapGGUF_ExplicitURL_DoesNotFallBack(t *testing.T) {
	fallbackHit := false
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		_, _ = w.Write([]byte("leaked"))
	}))
	defer good.Close()

	origFallbacks := FallbackGGUFURLs
	FallbackGGUFURLs = []string{good.URL}
	t.Cleanup(func() { FallbackGGUFURLs = origFallbacks })

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer bad.Close()

	_, err := BootstrapGGUF(context.Background(), BootstrapOptions{StateDir: t.TempDir(), URL: bad.URL})
	if err == nil {
		t.Fatal("expected pinned URL failure to surface, got nil error")
	}
	if fallbackHit {
		t.Fatal("fallback URL was consulted despite explicit opt.URL — chain must be skipped")
	}
}

// SHA-256 mismatch MUST abort the chain — we never want to silently pull
// a mirror whose bytes don't match an operator-supplied integrity pin.
func TestBootstrapGGUF_SHA256Mismatch_AbortsChain(t *testing.T) {
	fallbackHit := false
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit = true
		_, _ = w.Write([]byte("ignored"))
	}))
	defer good.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("real-bytes"))
	}))
	defer bad.Close()

	origDefault := DefaultGGUFURL_forTest_setValue(bad.URL)
	origFallbacks := FallbackGGUFURLs
	FallbackGGUFURLs = []string{good.URL}
	t.Cleanup(func() {
		DefaultGGUFURL_forTest_setValue(origDefault)
		FallbackGGUFURLs = origFallbacks
	})

	_, err := BootstrapGGUF(context.Background(), BootstrapOptions{
		StateDir: t.TempDir(),
		SHA256:   "deadbeef", // never matches
	})
	if err == nil {
		t.Fatal("expected sha mismatch to surface")
	}
	if fallbackHit {
		t.Fatal("fallback URL was consulted after sha mismatch — must abort the chain")
	}
}
