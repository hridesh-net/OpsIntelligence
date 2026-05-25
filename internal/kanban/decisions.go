// Package kanban implements decision-prompt detection and resume for human-in-the-loop.
package kanban

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// DecisionDetector scans agent output for questions that need human input.
type DecisionDetector struct {
	log *zap.Logger
}

// NewDecisionDetector creates a detector.
func NewDecisionDetector(log *zap.Logger) *DecisionDetector {
	if log == nil {
		log = zap.NewNop()
	}
	return &DecisionDetector{log: log}
}

// Detect scans text for decision prompts and returns a PendingDecision if found.
func (d *DecisionDetector) Detect(runID, cardID, text string) *datastore.PendingDecision {
	q := extractQuestion(text)
	if q == "" {
		return nil
	}
	return &datastore.PendingDecision{
		ID:       uuid.NewString(),
		RunID:    runID,
		CardID:   cardID,
		Question: q,
		Options:  extractOptions(text),
		Status:   "pending",
	}
}

var questionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:question|ask|confirm|should i|would you like|do you want|please choose|select one)\b[^.!?]*[?]`),
	regexp.MustCompile(`(?i)\b(?:A|B|C|D|E)[).:]\s+\S+`),
	regexp.MustCompile(`(?i)\b(?:yes|no|maybe)\b[^.!?]*[?]`),
}

func extractQuestion(text string) string {
	// Look for the last sentence that ends with ? and contains decision keywords.
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, re := range questionPatterns {
			if re.MatchString(line) {
				return line
			}
		}
		if strings.HasSuffix(line, "?") && len(line) > 10 {
			return line
		}
	}
	return ""
}

func extractOptions(text string) []string {
	var opts []string
	re := regexp.MustCompile(`(?im)^\s*(?:[-*•]|\d+[.)]|\w[.)])\s+(.+)$`)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			opts = append(opts, strings.TrimSpace(m[1]))
		}
	}
	return opts
}

// DecisionResume handles resuming runs after a decision is answered.
type DecisionResume struct {
	store datastore.Store
	log   *zap.Logger
	mu    sync.Mutex
	// resolvers maps decisionID -> channel that receives the answer.
	resolvers map[string]chan string
}

// NewDecisionResume creates a resume handler.
func NewDecisionResume(store datastore.Store, log *zap.Logger) *DecisionResume {
	if log == nil {
		log = zap.NewNop()
	}
	return &DecisionResume{
		store:     store,
		log:       log,
		resolvers: make(map[string]chan string),
	}
}

// Register creates a resolver channel for a pending decision.
func (r *DecisionResume) Register(decisionID string) chan string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan string, 1)
	r.resolvers[decisionID] = ch
	return ch
}

// Answer is called when a human answers a pending decision via the API.
func (r *DecisionResume) Answer(ctx context.Context, decisionID, answer string) error {
	if err := r.store.PendingDecisions().Answer(ctx, decisionID, answer); err != nil {
		return fmt.Errorf("store answer: %w", err)
	}
	r.mu.Lock()
	ch, ok := r.resolvers[decisionID]
	if ok {
		delete(r.resolvers, decisionID)
	}
	r.mu.Unlock()
	if ok {
		select {
		case ch <- answer:
		default:
		}
	}
	return nil
}
