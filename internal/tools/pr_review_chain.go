package tools

// pr_review_chain.go wires the prompts/chains/pr-review.yaml chain-of-thought
// pipeline into the ReviewFn so that /pr-review and devops.github.review_pr
// benefit from the multi-step gather → analyze → critique → render → post flow
// including methodology injection and repo intelligence context.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	gh "github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/prompts"
	"github.com/opsintelligence/opsintelligence/internal/repointel"
)

const prReviewChainID = "pr-review"

// chainPostPayload is the JSON the pr-review/post step emits.
type chainPostPayload struct {
	Event    string              `json:"event"`
	Body     string              `json:"body"`
	Comments []chainPostComment  `json:"comments"`
}

type chainPostComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// newChainReviewFn returns a ReviewFn that drives the pr-review chain-of-thought
// pipeline instead of the internal Go stage coordinator.
// It is only called when opts.PromptRunner is non-nil and the library contains
// the "pr-review" chain, so the caller already verified both preconditions.
func newChainReviewFn(ghClient *gh.Client, runner *prompts.Runner, opts ReviewFnOptions) ReviewFn {
	return func(ctx context.Context, owner, repo string, number int) (string, error) {
		prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)

		// ── Gather GitHub evidence ────────────────────────────────────────────
		pr, _ := ghClient.GetPullRequest(ctx, owner, repo, number)
		diff, _ := ghClient.GetPullRequestDiff(ctx, owner, repo, number)

		inputs := map[string]any{"pr_url": prURL}

		if pr != nil {
			prJSON, err := json.Marshal(pr)
			if err == nil {
				inputs["github_pr_json"] = string(prJSON)
			}
		}

		if diff != "" {
			const maxDiffBytes = 24_000
			if len(diff) > maxDiffBytes {
				diff = diff[:maxDiffBytes] + "\n... (truncated — diff exceeds 24 KB)"
			}
			inputs["github_diff"] = diff
		}

		// ── Inject methodology from disk ──────────────────────────────────────
		if opts.MethodologyPath != "" {
			if data, readErr := os.ReadFile(opts.MethodologyPath); readErr == nil {
				if s := strings.TrimSpace(string(data)); s != "" {
					inputs["methodology"] = s
				}
			}
		}

		// ── Inject repo intelligence context ─────────────────────────────────
		if opts.RepoManager != nil {
			repoID := repointel.RepoID("github", owner, repo)
			if repoCtx := opts.RepoManager.MemoryForReview(repoID); repoCtx != "" {
				inputs["repo_context"] = repoCtx
			}
		}

		// ── Run the chain ─────────────────────────────────────────────────────
		result, err := runner.RunChain(ctx, prReviewChainID, inputs)
		if err != nil {
			return "", fmt.Errorf("pr-review chain: %w", err)
		}

		finalOutput := strings.TrimSpace(result.Final)

		// ── Parse the post step's JSON payload and submit a formal review ─────
		var payload chainPostPayload
		if parseErr := json.Unmarshal([]byte(finalOutput), &payload); parseErr == nil && payload.Event != "" {
			rev := buildReviewRequest(pr, payload)
			if _, submitErr := ghClient.CreateReview(ctx, owner, repo, number, rev); submitErr != nil {
				if opts.Log != nil {
					opts.Log.Sugar().Warnf("pr-review chain: submit review failed: %v", submitErr)
				}
				return payload.Body, fmt.Errorf("pr-review chain: post review: %w", submitErr)
			}
			return payload.Body, nil
		}

		// Fallback: return the rendered markdown as a plain comment.
		if ghClient != nil && pr != nil {
			rev := gh.ReviewRequest{
				CommitID: pr.Head.SHA,
				Body:     finalOutput,
				Event:    "COMMENT",
			}
			_, _ = ghClient.CreateReview(ctx, owner, repo, number, rev)
		}
		return finalOutput, nil
	}
}

// buildReviewRequest converts the chain post payload into a GitHub ReviewRequest.
func buildReviewRequest(pr *gh.PullRequest, p chainPostPayload) gh.ReviewRequest {
	rev := gh.ReviewRequest{
		Body:  p.Body,
		Event: p.Event,
	}
	if pr != nil {
		rev.CommitID = pr.Head.SHA
	}
	for _, c := range p.Comments {
		side := c.Side
		if side == "" {
			side = "RIGHT"
		}
		line := c.Line
		if line <= 0 {
			line = 1
		}
		rev.Comments = append(rev.Comments, gh.ReviewComment{
			Path: c.Path,
			Line: line,
			Side: side,
			Body: c.Body,
		})
	}
	return rev
}
