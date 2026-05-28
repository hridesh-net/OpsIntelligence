package tuibridge

import (
	"context"
	"time"
)

// DoctorCheck is one diagnostic check result.
type DoctorCheck struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // ok | warn | error | skipped
	Message  string `json:"message"`
}

// DoctorOptions configures RunDoctor.
type DoctorOptions struct {
	// RunCheck executes the diagnostics. Called once in a goroutine.
	RunCheck func() []DoctorCheck
	LogDir   string
}

// RunDoctor launches the Rust doctor view, runs the supplied check function
// asynchronously, and shows results. Blocks until the user quits.
func RunDoctor(ctx context.Context, opts DoctorOptions) error {
	quit := make(chan struct{})
	handler := func(msg Message) {
		if msg.Method == "view.exit" {
			select {
			case <-quit:
			default:
				close(quit)
			}
		}
	}

	b, err := Spawn(ctx, Options{Handler: handler, LogDir: opts.LogDir})
	if err != nil {
		return err
	}
	defer func() { _ = b.Close(2 * time.Second) }()

	if err := b.Send("view.push", map[string]any{
		"view":   "doctor",
		"doctor": map[string]any{},
	}); err != nil {
		return err
	}
	// Initial "running" snapshot.
	_ = b.Send("doctor.snapshot", map[string]any{
		"running": true,
		"checks":  []DoctorCheck{},
	})

	// Kick off checks in a goroutine.
	resultsCh := make(chan []DoctorCheck, 1)
	go func() {
		if opts.RunCheck == nil {
			resultsCh <- nil
			return
		}
		resultsCh <- opts.RunCheck()
	}()

	for {
		select {
		case <-quit:
			return nil
		case <-b.Done():
			return b.Err()
		case <-ctx.Done():
			return ctx.Err()
		case checks := <-resultsCh:
			if checks == nil {
				checks = []DoctorCheck{}
			}
			_ = b.Send("doctor.snapshot", map[string]any{
				"running": false,
				"checks":  checks,
			})
			resultsCh = nil // drained; subsequent loop iterations just wait for quit
		}
	}
}
