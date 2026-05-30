// Package preview runs short-lived "branch preview" dev servers for a
// kanban card. Matches kanbots.dev's "branch preview" feature: launch the
// worktree's dev server, expose it on a free port, optionally fronted by
// Tailscale Funnel for a public HTTPS URL, and track it so the operator
// can stop / restart it from the board UI.
//
// Lifecycle is in-memory only — if the daemon restarts, previews are
// considered orphaned and reaped on first Get. Persistent previews
// belong in a separate "branch deploy" feature, not this card-scoped
// one.
package preview

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// Preview is the live state of one running dev-server preview.
type Preview struct {
	ID         string    `json:"id"`
	CardID     string    `json:"card_id"`
	WorktreePath string  `json:"worktree_path"`
	Cmd        string    `json:"cmd"`
	Port       int       `json:"port"`
	PID        int       `json:"pid"`
	LocalURL   string    `json:"local_url"`
	PublicURL  string    `json:"public_url,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	Status     string    `json:"status"` // "running" | "stopped" | "failed"
	LastError  string    `json:"last_error,omitempty"`

	process *exec.Cmd
	cancel  context.CancelFunc
	funnel  *exec.Cmd
}

// Manager owns the set of currently-running previews. Safe for concurrent
// use by HTTP / CLI / autopilot callers.
type Manager struct {
	mu       sync.Mutex
	previews map[string]*Preview
	byCard   map[string]string // cardID → preview ID (one preview per card)

	// FunnelEnabled, when true, attempts `tailscale funnel <port>` on the
	// host tailscaled. If the binary is missing or unauthenticated the
	// preview still starts; PublicURL just stays empty.
	FunnelEnabled bool
}

// New returns an empty Manager.
func New(funnelEnabled bool) *Manager {
	return &Manager{
		previews:      make(map[string]*Preview),
		byCard:        make(map[string]string),
		FunnelEnabled: funnelEnabled,
	}
}

// StartOpts configures Start. Cmd is required; Port=0 picks a free port.
type StartOpts struct {
	WorktreePath string
	Cmd          string
	Port         int
}

// Start launches a preview for a card. Only one preview per card is
// allowed; calling Start again on the same card returns an error so the
// operator must Stop the previous one first.
func (m *Manager) Start(ctx context.Context, cardID string, opts StartOpts) (*Preview, error) {
	m.mu.Lock()
	if existing, ok := m.byCard[cardID]; ok {
		if p, ok := m.previews[existing]; ok && p.Status == "running" {
			m.mu.Unlock()
			return nil, fmt.Errorf("preview: card %q already has a running preview (%s); stop it first", cardID, existing)
		}
	}
	m.mu.Unlock()

	if opts.Cmd == "" {
		return nil, fmt.Errorf("preview: cmd is required")
	}
	if opts.WorktreePath == "" {
		return nil, fmt.Errorf("preview: worktree_path is required")
	}
	if opts.Port == 0 {
		p, err := pickFreePort()
		if err != nil {
			return nil, fmt.Errorf("preview: pick port: %w", err)
		}
		opts.Port = p
	}

	parts := strings.Fields(opts.Cmd)
	if len(parts) == 0 {
		return nil, fmt.Errorf("preview: empty cmd")
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	c := exec.CommandContext(bgCtx, parts[0], parts[1:]...)
	c.Dir = opts.WorktreePath
	c.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", opts.Port))
	if err := c.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("preview: start: %w", err)
	}

	p := &Preview{
		ID:           uuid.NewString(),
		CardID:       cardID,
		WorktreePath: opts.WorktreePath,
		Cmd:          opts.Cmd,
		Port:         opts.Port,
		PID:          c.Process.Pid,
		LocalURL:     fmt.Sprintf("http://127.0.0.1:%d", opts.Port),
		StartedAt:    time.Now().UTC(),
		Status:       "running",
		process:      c,
		cancel:       cancel,
	}

	// Optionally front the port with Tailscale Funnel so the preview
	// gets a public *.ts.net HTTPS URL. Best-effort — preview still
	// works locally if Funnel isn't available.
	if m.FunnelEnabled {
		if url, fcmd, err := startFunnel(bgCtx, opts.Port); err == nil {
			p.PublicURL = url
			p.funnel = fcmd
		}
	}

	m.mu.Lock()
	m.previews[p.ID] = p
	m.byCard[cardID] = p.ID
	m.mu.Unlock()

	// Reaper: wait on the process so we can mark it stopped when the
	// dev server exits on its own (e.g. crashed, port collision).
	go func() {
		err := c.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		now := time.Now().UTC()
		p.StoppedAt = &now
		if err != nil && bgCtx.Err() == nil {
			p.Status = "failed"
			p.LastError = err.Error()
		} else {
			p.Status = "stopped"
		}
		if p.funnel != nil && p.funnel.Process != nil {
			_ = p.funnel.Process.Signal(syscall.SIGTERM)
		}
	}()

	return p, nil
}

// Stop terminates a running preview by ID.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	p, ok := m.previews[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("preview: unknown id %q", id)
	}
	if p.Status != "running" {
		return fmt.Errorf("preview: %q is %s", id, p.Status)
	}
	if p.cancel != nil {
		p.cancel()
	}
	if p.process != nil && p.process.Process != nil {
		_ = p.process.Process.Signal(syscall.SIGTERM)
	}
	return nil
}

// Get returns a copy of one preview, or nil if unknown.
func (m *Manager) Get(id string) *Preview {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.previews[id]; ok {
		cp := *p
		return &cp
	}
	return nil
}

// GetByCard returns the preview currently bound to the card, or nil.
func (m *Manager) GetByCard(cardID string) *Preview {
	m.mu.Lock()
	id, ok := m.byCard[cardID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return m.Get(id)
}

// List returns every preview the manager knows about (including stopped).
func (m *Manager) List() []Preview {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Preview, 0, len(m.previews))
	for _, p := range m.previews {
		out = append(out, *p)
	}
	return out
}

// ── helpers ────────────────────────────────────────────────────────────────

// pickFreePort asks the kernel for an unused TCP port by binding to :0
// then immediately closing. The brief race between close and the
// subprocess `listen()` is acceptable for a dev preview; if there's a
// collision the dev server fails and Status becomes "failed".
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// startFunnel runs `tailscale funnel <port>` and returns the public URL
// reported by the host's tailscaled. Failure is non-fatal: callers treat
// an empty URL as "no funnel, use the local one".
func startFunnel(ctx context.Context, port int) (string, *exec.Cmd, error) {
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return "", nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "funnel", fmt.Sprintf("%d", port))
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	// Quick-fetch the FQDN. Best-effort; the tailscale CLI writes the
	// URL to stdout but we suppress stdout, so we ask for it explicitly.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st := exec.CommandContext(probeCtx, bin, "status", "--json", "--peers=false")
	out, err := st.Output()
	if err != nil {
		return "", cmd, nil // funnel is running, URL just unknown
	}
	fqdn := extractTSFQDN(out)
	if fqdn == "" {
		return "", cmd, nil
	}
	return fmt.Sprintf("https://%s", fqdn), cmd, nil
}

func extractTSFQDN(jsonBytes []byte) string {
	// Tiny hand-rolled scan instead of pulling a JSON decoder dep — we
	// want one field: ${.Self.DNSName | trimSuffix "."}. The host name
	// always appears between `"DNSName":"` and the next `"`.
	const key = `"DNSName":"`
	s := string(jsonBytes)
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return strings.TrimSuffix(rest[:j], ".")
}
