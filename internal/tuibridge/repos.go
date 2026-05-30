package tuibridge

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/repointel"
)

// ReposOptions configures RunReposTUI.
type ReposOptions struct {
	Registry      *repointel.Registry
	MemoryDir     string
	Manager       *repointel.Manager // optional; used for live progress + sync
	OnSyncRequest func(id string)    // optional; if nil, only Manager.SyncRepo is called
	LogDir        string
}

// RunReposTUI launches the embedded Rust TUI in the Repo Intelligence view.
func RunReposTUI(ctx context.Context, opts ReposOptions) error {
	if opts.Registry == nil {
		return fmt.Errorf("tuibridge: ReposOptions.Registry is required")
	}

	st := &reposState{
		opts:     opts,
		entries:  opts.Registry.List(),
		progress: make(map[string]repointel.ProgressEvent),
	}
	st.refreshSelectedContent()

	quit := make(chan struct{})
	refreshReq := make(chan struct{}, 1)

	requestRefresh := func() {
		select {
		case refreshReq <- struct{}{}:
		default:
		}
	}

	requestQuit := func() {
		select {
		case <-quit:
		default:
			close(quit)
		}
	}

	var bridge *Bridge
	send := func(method string, params any) {
		if bridge != nil {
			_ = bridge.Send(method, params)
		}
	}

	handler := func(msg Message) {
		switch msg.Method {
		case "view.exit":
			requestQuit()
		case "repos.refresh":
			requestRefresh()
		case "repos.select":
			var p struct {
				Index int `json:"index"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			st.setSelected(p.Index)
			requestRefresh()
		case "repos.sync":
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil && p.ID != "" {
				if opts.Manager != nil {
					_ = opts.Manager.SyncRepo(p.ID)
				}
				if opts.OnSyncRequest != nil {
					opts.OnSyncRequest(p.ID)
				}
				requestRefresh()
			}
		case "repos.graph_select":
			var p struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil {
				st.setGraphSelected(p.ID)
				requestRefresh()
			}
		case "repos.edit_submit":
			var p struct {
				Architecture string `json:"architecture"`
				ReviewHints  string `json:"review_hints"`
				UserContext  string `json:"user_context"`
			}
			if err := json.Unmarshal(msg.Params, &p); err == nil {
				if err := st.saveMemoryEdit(p.Architecture, p.ReviewHints, p.UserContext); err != nil {
					send("repos.error", map[string]any{"message": err.Error()})
				}
				st.refreshSelectedContent()
				requestRefresh()
			}
		}
	}

	b, err := Spawn(ctx, Options{Handler: handler, LogDir: opts.LogDir})
	if err != nil {
		return err
	}
	bridge = b
	defer func() { _ = b.Close(2 * time.Second) }()

	if err := b.Send("view.push", map[string]any{
		"view":  "repos",
		"repos": map[string]any{"memory_dir": opts.MemoryDir},
	}); err != nil {
		return err
	}
	send("repos.snapshot", st.snapshot())

	// Subscribe to Manager.Progress (non-blocking; events are merged into state).
	if opts.Manager != nil {
		go st.subscribeProgress(ctx, opts.Manager, requestRefresh)
	}

	// Periodic registry reload (every 3s).
	tk := time.NewTicker(3 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-quit:
			return nil
		case <-b.Done():
			return b.CloseErr()
		case <-ctx.Done():
			return ctx.Err()
		case <-refreshReq:
			send("repos.snapshot", st.snapshot())
		case <-tk.C:
			st.reloadRegistry()
			send("repos.snapshot", st.snapshot())
		}
	}
}

// ── reposState ────────────────────────────────────────────────────────────

type reposState struct {
	mu       sync.Mutex
	opts     ReposOptions
	entries  []repointel.RepoEntry
	selected int

	memory    *repointel.RepoMemory
	scan      *repointel.ScanResult
	callGraph *repointel.CallGraph

	// selectedNodeID is the user's current node selection within the call
	// graph of the selected repo. Reset to "" whenever the selected repo
	// changes; the renderer then falls back to the first node.
	selectedNodeID string

	progress map[string]repointel.ProgressEvent
}

func (s *reposState) setSelected(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s.entries) {
		idx = len(s.entries) - 1
	}
	if idx < 0 {
		idx = 0
	}
	s.selected = idx
	s.unlockedRefreshSelectedContent()
}

func (s *reposState) setGraphSelected(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedNodeID = nodeID
}

func (s *reposState) reloadRegistry() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.opts.Registry.Reload()
	s.entries = s.opts.Registry.List()
	if s.selected >= len(s.entries) {
		s.selected = 0
	}
	s.unlockedRefreshSelectedContent()
}

func (s *reposState) refreshSelectedContent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unlockedRefreshSelectedContent()
}

func (s *reposState) unlockedRefreshSelectedContent() {
	s.memory = nil
	s.scan = nil
	s.callGraph = nil
	// Reset the per-repo graph cursor when the active repo changes.
	s.selectedNodeID = ""
	if s.selected >= len(s.entries) {
		return
	}
	e := s.entries[s.selected]
	if e.MemoryFile != "" {
		if m, err := repointel.LoadMemory(filepath.Join(s.opts.MemoryDir, e.MemoryFile)); err == nil {
			s.memory = m
		}
	}
	if e.ScanFile != "" {
		if sc, err := repointel.LoadScan(filepath.Join(s.opts.MemoryDir, e.ScanFile)); err == nil {
			s.scan = sc
		}
	}
	if e.CallGraphFile != "" {
		if cg, err := repointel.LoadCallGraph(filepath.Join(s.opts.MemoryDir, e.CallGraphFile)); err == nil {
			s.callGraph = cg
		}
	}
}

func (s *reposState) saveMemoryEdit(arch, hints, userCtx string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected >= len(s.entries) {
		return fmt.Errorf("no repo selected")
	}
	entry := s.entries[s.selected]
	if entry.MemoryFile == "" {
		return fmt.Errorf("no memory file path stored for repo")
	}
	if s.memory == nil {
		// Try to load fresh.
		m, err := repointel.LoadMemory(filepath.Join(s.opts.MemoryDir, entry.MemoryFile))
		if err != nil {
			return fmt.Errorf("memory not loaded: %w", err)
		}
		s.memory = m
	}
	mem := *s.memory
	mem.Architecture = arch
	mem.ReviewHints = hints
	mem.UserContext = userCtx
	mem.UpdatedAt = time.Now().UTC()
	abs := filepath.Join(s.opts.MemoryDir, entry.MemoryFile)
	if err := repointel.SaveMemory(abs, &mem); err != nil {
		return err
	}
	s.memory = &mem
	return nil
}

func (s *reposState) subscribeProgress(ctx context.Context, mgr *repointel.Manager, refresh func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-mgr.Progress:
			if !ok {
				return
			}
			s.mu.Lock()
			s.progress[ev.RepoID] = ev
			s.mu.Unlock()
			refresh()
		}
	}
}

// ── Snapshot ──────────────────────────────────────────────────────────────

type reposSnapshot struct {
	Entries  []repoEntryJSON `json:"entries"`
	Selected int             `json:"selected"`
	Memory   *memoryJSON     `json:"memory,omitempty"`
	Scan     *scanJSON       `json:"scan,omitempty"`
	Users    []userJSON      `json:"users"`
	Graph    *graphJSON      `json:"graph,omitempty"`
}

type repoEntryJSON struct {
	ID            string        `json:"id"`
	FullName      string        `json:"full_name"`
	Platform      string        `json:"platform"`
	Language      string        `json:"language"`
	Description   string        `json:"description"`
	IndexStatus   string        `json:"index_status"`
	ScanStatus    string        `json:"scan_status"`
	RiskLevel     string        `json:"risk_level"`
	IndexedAt     string        `json:"indexed_at"`
	HeadSHA       string        `json:"head_sha"`
	TreeTruncated bool          `json:"tree_truncated"`
	UsersCount    int           `json:"users_count"`
	Progress      *progressJSON `json:"progress,omitempty"`
	IndexError    string        `json:"index_error"`
	ScanError     string        `json:"scan_error"`
}

type progressJSON struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Pct     int    `json:"pct"`
}

type memoryJSON struct {
	UpdatedAt    string        `json:"updated_at"`
	Architecture string        `json:"architecture"`
	PrimaryLang  string        `json:"primary_lang"`
	Languages    []string      `json:"languages"`
	KeyFiles     []string      `json:"key_files"`
	Conventions  []nameValueJS `json:"conventions"`
	Dependencies []nameValueJS `json:"dependencies"`
	TestPatterns string        `json:"test_patterns"`
	CISummary    string        `json:"ci_summary"`
	ReviewHints  string        `json:"review_hints"`
	CommonIssues []string      `json:"common_issues"`
	UserContext  string        `json:"user_context"`
}

type nameValueJS struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type scanJSON struct {
	ScannedAt   string        `json:"scanned_at"`
	RiskLevel   string        `json:"risk_level"`
	Summary     string        `json:"summary"`
	CVEs        []findingJSON `json:"cves"`
	Bottlenecks []findingJSON `json:"bottlenecks"`
	Suggestions []findingJSON `json:"suggestions"`
}

type findingJSON struct {
	Severity    string   `json:"severity"`
	Location    string   `json:"location,omitempty"`
	Package     string   `json:"package,omitempty"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description"`
	Fix         string   `json:"fix,omitempty"`
	CVEIDs      []string `json:"cve_ids,omitempty"`
}

type userJSON struct {
	Login string `json:"login"`
	Role  string `json:"role"`
}

type graphJSON struct {
	NodeCount    int            `json:"node_count"`
	EdgeCount    int            `json:"edge_count"`
	Selected     *callNodeJSON  `json:"selected,omitempty"`
	SelectedIdx  int            `json:"selected_idx"`
	Nodes        []callNodeJSON `json:"nodes"`
	Callees      []callNodeJSON `json:"callees"`
	Callers      []callNodeJSON `json:"callers"`
}

type callNodeJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Package string `json:"package,omitempty"`
}

func (s *reposState) snapshot() reposSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := make([]repoEntryJSON, 0, len(s.entries))
	for _, e := range s.entries {
		ej := repoEntryJSON{
			ID:            e.ID,
			FullName:      e.FullName,
			Platform:      e.Platform,
			Language:      e.Language,
			Description:   e.Description,
			IndexStatus:   string(e.IndexStatus),
			ScanStatus:    string(e.ScanStatus),
			RiskLevel:     e.RiskLevel,
			IndexedAt:     fmtTimeShort(e.IndexedAt),
			HeadSHA:       shortSHA(e.HeadSHA),
			TreeTruncated: e.IndexTreeTruncated,
			UsersCount:    len(e.Users),
			IndexError:    e.IndexError,
			ScanError:     e.ScanError,
		}
		if ev, ok := s.progress[e.ID]; ok {
			ej.Progress = &progressJSON{
				Kind:    string(ev.Kind),
				Message: ev.Message,
				Pct:     ev.Pct(),
			}
		}
		entries = append(entries, ej)
	}

	var memOut *memoryJSON
	if s.memory != nil {
		conv := make([]nameValueJS, 0, len(s.memory.Conventions))
		for _, c := range s.memory.Conventions {
			conv = append(conv, nameValueJS{Name: c.Name, Value: c.Pattern})
		}
		deps := make([]nameValueJS, 0, len(s.memory.Dependencies))
		for _, d := range s.memory.Dependencies {
			val := d.Version
			if val != "" && d.Purpose != "" {
				val += " — " + d.Purpose
			} else if val == "" {
				val = d.Purpose
			}
			deps = append(deps, nameValueJS{Name: d.Name, Value: val})
		}
		memOut = &memoryJSON{
			UpdatedAt:    fmtTimeShort(s.memory.UpdatedAt),
			Architecture: s.memory.Architecture,
			PrimaryLang:  s.memory.PrimaryLang,
			Languages:    s.memory.Languages,
			KeyFiles:     s.memory.KeyFiles,
			Conventions:  conv,
			Dependencies: deps,
			TestPatterns: s.memory.TestPatterns,
			CISummary:    s.memory.CISummary,
			ReviewHints:  s.memory.ReviewHints,
			CommonIssues: s.memory.CommonIssues,
			UserContext:  s.memory.UserContext,
		}
	}

	var scanOut *scanJSON
	if s.scan != nil {
		scanOut = &scanJSON{
			ScannedAt:   fmtTimeShort(s.scan.ScannedAt),
			RiskLevel:   s.scan.RiskLevel,
			Summary:     s.scan.Summary,
			CVEs:        toCveFindings(s.scan.CVEs),
			Bottlenecks: toBottleneckFindings(s.scan.Bottlenecks),
			Suggestions: toSuggestionFindings(s.scan.Suggestions),
		}
	}

	users := []userJSON{}
	if s.selected < len(s.entries) {
		for _, u := range s.entries[s.selected].Users {
			users = append(users, userJSON{Login: u.Handle, Role: string(u.Role)})
		}
	}

	var graphOut *graphJSON
	if s.callGraph != nil {
		graphOut = &graphJSON{
			NodeCount: len(s.callGraph.Nodes),
			EdgeCount: len(s.callGraph.Edges),
		}
		// Cap the node list so very large graphs don't blow up the JSON
		// payload. 200 is plenty for navigation; the user can still pick any
		// node beyond this via search / sync.
		const nodeListCap = 200
		idIdx := map[string]repointel.CallNode{}
		for _, nn := range s.callGraph.Nodes {
			idIdx[nn.ID] = nn
		}
		for i, nn := range s.callGraph.Nodes {
			if i >= nodeListCap {
				break
			}
			graphOut.Nodes = append(graphOut.Nodes, callNodeJSON{
				ID: nn.ID, Name: nn.Name, Kind: nn.Kind,
				File: nn.File, Line: nn.Line, Package: nn.Package,
			})
		}
		// Pick selected node: explicit selectedNodeID > index 0.
		picked := -1
		if s.selectedNodeID != "" {
			for i, nn := range s.callGraph.Nodes {
				if nn.ID == s.selectedNodeID {
					picked = i
					break
				}
			}
		}
		if picked < 0 && len(s.callGraph.Nodes) > 0 {
			picked = 0
		}
		if picked >= 0 {
			n := s.callGraph.Nodes[picked]
			graphOut.Selected = &callNodeJSON{
				ID:      n.ID,
				Name:    n.Name,
				Kind:    n.Kind,
				File:    n.File,
				Line:    n.Line,
				Package: n.Package,
			}
			graphOut.SelectedIdx = picked
			for _, id := range s.callGraph.Callees(n.ID) {
				if nn, ok := idIdx[id]; ok {
					graphOut.Callees = append(graphOut.Callees, callNodeJSON{
						ID: nn.ID, Name: nn.Name, Kind: nn.Kind,
						File: nn.File, Line: nn.Line, Package: nn.Package,
					})
				}
			}
			for _, id := range s.callGraph.Callers(n.ID) {
				if nn, ok := idIdx[id]; ok {
					graphOut.Callers = append(graphOut.Callers, callNodeJSON{
						ID: nn.ID, Name: nn.Name, Kind: nn.Kind,
						File: nn.File, Line: nn.Line, Package: nn.Package,
					})
				}
			}
		}
	}

	return reposSnapshot{
		Entries:  entries,
		Selected: s.selected,
		Memory:   memOut,
		Scan:     scanOut,
		Users:    users,
		Graph:    graphOut,
	}
}

// ── Converters ────────────────────────────────────────────────────────────

func toCveFindings(in []repointel.CVEFinding) []findingJSON {
	out := make([]findingJSON, 0, len(in))
	for _, f := range in {
		out = append(out, findingJSON{
			Severity:    f.Severity,
			Package:     f.Package,
			Version:     f.Version,
			Description: f.Description,
			Fix:         f.Fix,
			CVEIDs:      f.CVEIDs,
		})
	}
	return out
}

func toBottleneckFindings(in []repointel.BottleneckFinding) []findingJSON {
	out := make([]findingJSON, 0, len(in))
	for _, f := range in {
		out = append(out, findingJSON{
			Severity:    f.Severity,
			Location:    f.Location,
			Description: f.Description,
			Fix:         f.Fix,
		})
	}
	return out
}

func toSuggestionFindings(in []repointel.ArchitectureSuggestion) []findingJSON {
	out := make([]findingJSON, 0, len(in))
	for _, s := range in {
		out = append(out, findingJSON{
			Severity:    s.Priority,
			Location:    s.Area,
			Description: s.Suggestion,
		})
	}
	return out
}

func fmtTimeShort(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func shortSHA(s string) string {
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}
