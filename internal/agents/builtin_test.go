package agents

import "testing"

// The PR review specialist must default to POSTING a review (via review_pr),
// not stay read-only. Regression guard for the bug where it narrated a review
// in chat but never posted inline comments to the PR.
func TestPRReviewAgent_postsByDefault(t *testing.T) {
	def := NewPRReviewAgent(PRReviewOpts{})
	p := def.SystemPromptFocus

	if !contains(p, "devops.github.review_pr") {
		t.Fatal("pr_review prompt must name devops.github.review_pr as the posting tool")
	}
	// The old read-only gate caused the no-post bug — it must be gone.
	if contains(p, "Stay read-only until explicitly told to post") {
		t.Fatal("pr_review prompt still contains the read-only-until-told gate that suppressed posting")
	}
	// review_pr must be reachable from the specialist's toolset.
	found := false
	for _, tool := range def.AllowedTools {
		if tool == "devops.github.review_pr" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("pr_review AllowedTools must include devops.github.review_pr")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
