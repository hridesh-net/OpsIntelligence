package tools

import "testing"

func TestResolveOwnerRepoURL(t *testing.T) {
	// A full PR URL supplies owner/repo/number even with no default_org.
	o, r, n, err := resolveOwnerRepoURL("", "", "https://github.com/pashuvaani/pashuvaani26/pull/23", "")
	if err != nil || o != "pashuvaani" || r != "pashuvaani26" || n != 23 {
		t.Fatalf("pr_url: got (%q,%q,%d,%v), want pashuvaani/pashuvaani26/23", o, r, n, err)
	}

	// A URL mistakenly placed in the repo field still resolves.
	o, r, n, err = resolveOwnerRepoURL("", "https://github.com/a/b/pull/7", "", "")
	if err != nil || o != "a" || r != "b" || n != 7 {
		t.Fatalf("url-in-repo: got (%q,%q,%d,%v)", o, r, n, err)
	}

	// Explicit owner/repo still works; number comes from the caller (0 here).
	o, r, n, err = resolveOwnerRepoURL("acme", "web", "", "")
	if err != nil || o != "acme" || r != "web" || n != 0 {
		t.Fatalf("explicit: got (%q,%q,%d,%v)", o, r, n, err)
	}

	// No owner, no URL, no default_org → actionable error.
	if _, _, _, err = resolveOwnerRepoURL("", "", "", ""); err == nil {
		t.Fatal("expected error when owner/repo/url all empty")
	}
}
