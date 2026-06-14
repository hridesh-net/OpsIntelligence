package agent

import (
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

func tcr(name string, input any) toolCallResult {
	return toolCallResult{tc: provider.ContentPart{ToolName: name, ToolInput: input}}
}

// The model re-issuing the exact same call (name + input) must trip on the
// loopBreakRepeats-th occurrence — this is the "forgot a required arg and
// retried forever" case the guard exists to stop.
func TestLoopGuard_tripsOnIdenticalRepeat(t *testing.T) {
	g := newLoopGuard()
	call := []toolCallResult{tcr("devops.github.workflow_runs", map[string]any{"repo": "OpsIntelligence"})}

	for i := 1; i < loopBreakRepeats; i++ {
		if tripped, _, _ := g.record(call); tripped {
			t.Fatalf("tripped too early at occurrence %d (limit %d)", i, loopBreakRepeats)
		}
	}
	tripped, name, n := g.record(call)
	if !tripped {
		t.Fatalf("expected guard to trip on occurrence %d", loopBreakRepeats)
	}
	if name != "devops.github.workflow_runs" || n != loopBreakRepeats {
		t.Fatalf("got name=%q count=%d, want devops.github.workflow_runs / %d", name, n, loopBreakRepeats)
	}
}

// Different inputs to the same tool are progress, not a loop — must not trip.
func TestLoopGuard_distinctInputsDoNotTrip(t *testing.T) {
	g := newLoopGuard()
	for i := 0; i < loopBreakRepeats+2; i++ {
		call := []toolCallResult{tcr("devops.github.workflow_runs", map[string]any{"owner": "acme", "repo": i})}
		if tripped, _, _ := g.record(call); tripped {
			t.Fatalf("tripped on distinct input at iteration %d", i)
		}
	}
}

// Distinct signatures are counted independently and must not be merged into a
// single total. Two different tools each seen (limit-1) times should leave both
// under the threshold — if counts merged, the combined total would trip.
func TestLoopGuard_distinctSignaturesCountedIndependently(t *testing.T) {
	g := newLoopGuard()
	for i := 0; i < loopBreakRepeats-1; i++ {
		if tripped, _, _ := g.record([]toolCallResult{tcr("repo.search", map[string]any{"q": "x"})}); tripped {
			t.Fatalf("repo.search tripped early at round %d", i)
		}
		if tripped, _, _ := g.record([]toolCallResult{tcr("repo.read", map[string]any{"p": "y"})}); tripped {
			t.Fatalf("repo.read tripped early at round %d", i)
		}
	}
}
