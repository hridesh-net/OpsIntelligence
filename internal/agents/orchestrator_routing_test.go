package agents

import "testing"

func TestParseGitHubPRURL(t *testing.T) {
	cases := []struct {
		in            string
		owner, repo   string
		number        int
		ok            bool
	}{
		{"review https://github.com/pashuvaani/pashuvaani26/pull/23 please", "pashuvaani", "pashuvaani26", 23, true},
		{"https://github.com/owner/repo/pull/42#issue-123", "owner", "repo", 42, true},
		{"github.com/a/b/pull/7/", "a", "b", 7, true},
		{"review the latest PR", "", "", 0, false},
		{"https://github.com/owner/repo/issues/9", "", "", 0, false},
		{"https://gitlab.com/owner/repo/-/merge_requests/3", "", "", 0, false},
	}
	for _, c := range cases {
		owner, repo, number, ok := parseGitHubPRURL(c.in)
		if ok != c.ok || owner != c.owner || repo != c.repo || number != c.number {
			t.Errorf("parseGitHubPRURL(%q) = (%q,%q,%d,%v), want (%q,%q,%d,%v)",
				c.in, owner, repo, number, ok, c.owner, c.repo, c.number, c.ok)
		}
	}
}

func TestMentionsNoPost(t *testing.T) {
	post := []string{
		"review this PR and post inline comments https://github.com/a/b/pull/1",
		"review https://github.com/a/b/pull/1 with suggested fixes",
	}
	noPost := []string{
		"dry run a review of https://github.com/a/b/pull/1",
		"just summarize the PR, don't post",
		"give me a read-only review",
	}
	for _, q := range post {
		if mentionsNoPost(q) {
			t.Errorf("mentionsNoPost(%q) = true, want false (should post)", q)
		}
	}
	for _, q := range noPost {
		if !mentionsNoPost(q) {
			t.Errorf("mentionsNoPost(%q) = false, want true (should NOT post)", q)
		}
	}
}
