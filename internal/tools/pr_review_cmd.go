package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/channels"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
	"go.uber.org/zap"
)

// PRLink holds a parsed GitHub PR reference extracted from a /pr-review command.
type PRLink struct {
	Owner  string
	Repo   string
	Number int
	Raw    string // original URL
}

var ghPRRe = regexp.MustCompile(`https?://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)`)

// ParsePRLinks scans text for GitHub PR URLs and returns one entry per unique PR found.
func ParsePRLinks(text string) []PRLink {
	matches := ghPRRe.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	var out []PRLink
	for _, m := range matches {
		key := m[1] + "/" + m[2] + "#" + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		n, _ := strconv.Atoi(m[3])
		out = append(out, PRLink{Owner: m[1], Repo: m[2], Number: n, Raw: m[0]})
	}
	return out
}

// ReviewFn performs a PR review and returns a human-readable result string.
type ReviewFn func(ctx context.Context, owner, repo string, number int) (string, error)

// prReviewWork is JSON-encoded as the task prompt given to the TaskManager.
type prReviewWork struct {
	PR          PRLink `json:"pr"`
	ChannelID   string `json:"channel_id"`
	SessionID   string `json:"session_id"`
	OriginMsgID string `json:"origin_msg_id"`
}

const prReviewAgentID = "pr-reviewer"

// PRReviewCmdHandler orchestrates parallel PR reviews triggered by /pr-review channel commands.
//
// It owns a TaskManager whose MaxConcurrent slot limits how many reviews run simultaneously;
// excess tasks queue automatically. Each task emits structured progress events visible on
// the master agent dashboard and queryable via the pr_review_tasks / pr_review_events tools.
//
// Use NewPRReviewCmdHandler to construct; do not create the struct directly.
type PRReviewCmdHandler struct {
	mgr     *subagents.TaskManager
	Review  ReviewFn
	Senders map[string]ChannelSender
	Log     *zap.Logger
}

// NewPRReviewCmdHandler builds a handler with bounded concurrency.
// maxWorkers <= 0 defaults to 4.
func NewPRReviewCmdHandler(reviewFn ReviewFn, senders map[string]ChannelSender, maxWorkers int, log *zap.Logger) *PRReviewCmdHandler {
	if maxWorkers <= 0 {
		maxWorkers = 4
	}
	h := &PRReviewCmdHandler{
		Review:  reviewFn,
		Senders: senders,
		Log:     log,
	}
	exec := func(ctx context.Context, taskID, _, taskPrompt string) (string, int, error) {
		var work prReviewWork
		if err := json.Unmarshal([]byte(taskPrompt), &work); err != nil {
			return "", 0, fmt.Errorf("pr-reviewer: malformed task prompt: %w", err)
		}
		pr := work.PR

		h.report(taskID, "fetch", fmt.Sprintf("fetching diff for %s", pr.Raw))
		result, err := h.Review(ctx, pr.Owner, pr.Repo, pr.Number)

		var body string
		if err != nil {
			body = fmt.Sprintf("PR review failed for %s\n\nError: %v", pr.Raw, err)
			if h.Log != nil {
				h.Log.Error("pr review failed", zap.String("pr", pr.Raw), zap.Error(err))
			}
			h.report(taskID, "error", fmt.Sprintf("review failed: %v", err))
		} else {
			h.report(taskID, "done", "review submitted to GitHub")
			body = fmt.Sprintf("**PR Review: %s**\n\n%s", pr.Raw, result)
		}
		h.sendResult(ctx, work.ChannelID, work.SessionID, work.OriginMsgID, body)
		return body, 1, err
	}

	mgr := subagents.NewTaskManager(exec)
	mgr.MaxConcurrent = maxWorkers
	mgr.DefaultTimeout = 30 * time.Minute
	mgr.RetainLimit = 128
	h.mgr = mgr
	return h
}

// Dispatch implements agent.PRReviewDispatcher — called by the runner's /pr-review handler.
func (h *PRReviewCmdHandler) Dispatch(ctx context.Context, commandText, channelID, sessionID, originMsgID string, replyFn channels.StreamingReplyFunc) {
	h.LaunchReviews(ctx, ParsePRLinks(commandText), channelID, sessionID, originMsgID, replyFn)
}

// LaunchReviews acknowledges the batch immediately then enqueues one task per PR link.
// Tasks beyond MaxConcurrent queue automatically; workers are picked up as slots free.
func (h *PRReviewCmdHandler) LaunchReviews(
	ctx context.Context,
	links []PRLink,
	channelID, sessionID, originMsgID string,
	replyFn channels.StreamingReplyFunc,
) {
	if len(links) == 0 {
		_ = replyFn("No GitHub PR links found. Usage: /pr-review: https://github.com/owner/repo/pull/N")
		return
	}

	workers := h.mgr.MaxConcurrent
	if workers <= 0 {
		workers = 4
	}
	label := "1 PR"
	if len(links) > 1 {
		label = fmt.Sprintf("%d PRs", len(links))
	}
	_ = replyFn(fmt.Sprintf(
		"Queued %s for review (up to %d running in parallel). Results will be posted as replies when ready.",
		label, workers,
	))

	for _, link := range links {
		work := prReviewWork{
			PR:          link,
			ChannelID:   channelID,
			SessionID:   sessionID,
			OriginMsgID: originMsgID,
		}
		taskPrompt, _ := json.Marshal(work)
		displayName := fmt.Sprintf("PR Review: %s/%s#%d", link.Owner, link.Repo, link.Number)
		if _, err := h.mgr.RunAsync(prReviewAgentID, displayName, string(taskPrompt), 0); err != nil {
			if h.Log != nil {
				h.Log.Error("failed to queue pr review", zap.String("pr", link.Raw), zap.Error(err))
			}
		}
	}
}

// StatusReport returns a formatted multi-line status of all known PR review tasks,
// suitable for the /pr-reviews CLI command and agent tool output.
func (h *PRReviewCmdHandler) StatusReport() string {
	tasks := h.mgr.List()
	if len(tasks) == 0 {
		return "No PR review tasks. Use /pr-review: <url> to start one."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("PR Review Tasks (%d total, max %d concurrent)\n\n", len(tasks), h.mgr.MaxConcurrent))
	for _, t := range tasks {
		elapsed := t.Elapsed().Round(time.Second)
		b.WriteString(fmt.Sprintf("• task=%-12s  status=%-10s  elapsed=%s\n  %s\n",
			t.ID, t.Status, elapsed, t.SubAgentNm))
		if last := t.LastEvent(); last.Message != "" {
			b.WriteString(fmt.Sprintf("  last: [%s] %s\n", last.Phase, last.Message))
		}
		if t.Error != "" {
			b.WriteString(fmt.Sprintf("  error: %s\n", t.Error))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Dashboard returns a compact block for injection into the master agent's system prompt.
// Returns "" when there are no active (pending/running) tasks.
func (h *PRReviewCmdHandler) Dashboard() string {
	active := h.mgr.Active()
	if len(active) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Active PR Reviews (%d/%d slots used)\n", len(active), h.mgr.MaxConcurrent))
	for _, t := range active {
		b.WriteString(fmt.Sprintf("  • task=%-12s  status=%-10s  elapsed=%s  %s\n",
			t.ID, t.Status, t.Elapsed().Round(time.Second), t.SubAgentNm))
		if last := t.LastEvent(); last.Message != "" {
			b.WriteString(fmt.Sprintf("    last: [%s] %s\n", last.Phase, last.Message))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Manager exposes the underlying TaskManager for tool registration (pr_review_tasks etc.).
func (h *PRReviewCmdHandler) Manager() *subagents.TaskManager { return h.mgr }

func (h *PRReviewCmdHandler) report(taskID, phase, msg string) {
	if h.mgr != nil {
		_ = h.mgr.Report(taskID, phase, msg, subagents.KindProgress)
	}
}

func (h *PRReviewCmdHandler) sendResult(ctx context.Context, channelID, sessionID, originMsgID, text string) {
	sender, ok := h.Senders[channelID]
	if !ok {
		return
	}
	if r, ok := sender.(ChannelReplier); ok && originMsgID != "" {
		if err := r.ReplyTo(ctx, sessionID, originMsgID, text); err == nil {
			return
		}
	}
	_ = sender.SendText(ctx, sessionID, text)
}
