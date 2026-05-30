package kanban

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// BudgetConfig is parsed from a board's config_json. All ceilings are USD
// dollars (float). Zero means "no cap" for that scope.
type BudgetConfig struct {
	// PerCardUSD: the maximum total cost ever accrued by all runs against
	// a single card. The dispatcher refuses to start a new run if the
	// card has already accumulated more than this.
	PerCardUSD float64 `json:"per_card_usd"`
	// PerBoardUSD: the same ceiling but summed across every card on the
	// board. Useful for shared team budgets.
	PerBoardUSD float64 `json:"per_board_usd"`
	// PerRunUSD: the cost of a single run, enforced inside runAgent by
	// streaming tokens through the cost calculator on each event flush.
	PerRunUSD float64 `json:"per_run_usd"`
}

// parseBudget pulls the board's BudgetConfig out of its config map.
// Returns a zero-value config (no caps) when the map is empty or missing
// the "budget" key — callers can treat that as "unlimited".
func parseBudget(board *datastore.Board) BudgetConfig {
	if board == nil || len(board.Config) == 0 {
		return BudgetConfig{}
	}
	raw, ok := board.Config["budget"]
	if !ok {
		return BudgetConfig{}
	}
	// Round-trip through JSON so we honour the canonical field names
	// without writing a manual type switch for each scalar.
	b, err := json.Marshal(raw)
	if err != nil {
		return BudgetConfig{}
	}
	var out BudgetConfig
	if err := json.Unmarshal(b, &out); err != nil {
		return BudgetConfig{}
	}
	return out
}

// enforceBudget refuses the dispatch if either the per-card or per-board
// budget is already exceeded. Per-run budget is enforced separately inside
// runAgent (we need streaming token counts to check it during the run).
func (s *DispatchService) enforceBudget(ctx context.Context, card *datastore.BoardCard, board *datastore.Board) error {
	cfg := parseBudget(board)
	if cfg.PerCardUSD > 0 && card.CostUSD >= cfg.PerCardUSD {
		return fmt.Errorf(
			"dispatch refused: card %q has accrued $%.4f, which exceeds the per-card budget of $%.4f (board %q)",
			card.ID, card.CostUSD, cfg.PerCardUSD, board.ID,
		)
	}
	if cfg.PerBoardUSD > 0 {
		total, err := s.totalBoardCost(ctx, board.ID)
		if err == nil && total >= cfg.PerBoardUSD {
			return fmt.Errorf(
				"dispatch refused: board %q has accrued $%.4f, which exceeds the per-board budget of $%.4f",
				board.ID, total, cfg.PerBoardUSD,
			)
		}
	}
	return nil
}

// totalBoardCost sums CostUSD across every card on the board. SQLite handles
// a few hundred cards comfortably; if boards grow much larger we'll add a
// running-total column on `boards` and increment it transactionally.
func (s *DispatchService) totalBoardCost(ctx context.Context, boardID string) (float64, error) {
	cards, err := s.Store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: boardID})
	if err != nil {
		return 0, err
	}
	var total float64
	for _, c := range cards {
		total += c.CostUSD
	}
	return total, nil
}

// perRunCap returns the per-run dollar ceiling for this board (0 = none).
// Used by runAgent to short-circuit a runaway agent mid-stream.
func (s *DispatchService) perRunCap(board *datastore.Board) float64 {
	return parseBudget(board).PerRunUSD
}
