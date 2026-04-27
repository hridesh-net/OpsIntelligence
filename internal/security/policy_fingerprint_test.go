package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyBundleFingerprint_empty(t *testing.T) {
	if got := PolicyBundleFingerprint(""); got != "" {
		t.Fatalf("empty stateDir: got %q", got)
	}
	dir := t.TempDir()
	if got := PolicyBundleFingerprint(dir); got != "" {
		t.Fatalf("no policy files: got %q", got)
	}
}

func TestPolicyBundleFingerprint_stable(t *testing.T) {
	dir := t.TempDir()
	pol := filepath.Join(dir, "POLICIES.md")
	if err := os.WriteFile(pol, []byte("owner rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	teamDir := filepath.Join(dir, "teams", "alpha")
	if err := os.MkdirAll(teamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(teamDir, "ci.md"), []byte("ci policy"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := PolicyBundleFingerprint(dir)
	b := PolicyBundleFingerprint(dir)
	if a == "" || a != b {
		t.Fatalf("fingerprint unstable: %q vs %q", a, b)
	}
}
