package repointel

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ── Registry ──────────────────────────────────────────────────────────────────

// Registry is a YAML-backed, mutex-protected store of RepoEntry records.
// The file is re-written atomically on every mutation.
type Registry struct {
	mu   sync.RWMutex
	path string            // absolute path to repos.yaml
	data map[string]*RepoEntry // keyed by RepoEntry.ID
}

// registryFile is the on-disk representation.
type registryFile struct {
	Repos []*RepoEntry `yaml:"repos"`
}

// NewRegistry opens (or creates) the registry file at path.
func NewRegistry(path string) (*Registry, error) {
	r := &Registry{
		path: path,
		data: make(map[string]*RepoEntry),
	}
	if err := r.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("repointel: open registry %s: %w", path, err)
	}
	return r, nil
}

// ── Reads ─────────────────────────────────────────────────────────────────────

// Get returns a copy of the entry with the given ID, or an error if not found.
func (r *Registry) Get(id string) (RepoEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.data[id]
	if !ok {
		return RepoEntry{}, fmt.Errorf("repointel: repo %q not found", id)
	}
	return *e, nil
}

// List returns a snapshot of all entries, sorted by AddedAt ascending.
func (r *Registry) List() []RepoEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RepoEntry, 0, len(r.data))
	for _, e := range r.data {
		out = append(out, *e)
	}
	// stable sort by AddedAt
	sortByAddedAt(out)
	return out
}

// ── Mutations ─────────────────────────────────────────────────────────────────

// Add inserts a new repo entry. Returns an error if the ID already exists.
func (r *Registry) Add(e RepoEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.data[e.ID]; exists {
		return fmt.Errorf("repointel: repo %q already registered", e.ID)
	}
	if e.AddedAt.IsZero() {
		e.AddedAt = time.Now()
	}
	if e.IndexStatus == "" {
		e.IndexStatus = IndexPending
	}
	if e.ScanStatus == "" {
		e.ScanStatus = ScanPending
	}
	r.data[e.ID] = &e
	return r.save()
}

// Remove deletes a repo entry by ID.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[id]; !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	delete(r.data, id)
	return r.save()
}

// UpdateIndexStatus sets the indexing state fields for a repo.
func (r *Registry) UpdateIndexStatus(id string, status IndexStatus, headSHA, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.IndexStatus = status
	e.IndexError = errMsg
	if headSHA != "" {
		e.HeadSHA = headSHA
	}
	if status == IndexReady {
		e.IndexedAt = time.Now()
	}
	return r.save()
}

// UpdateScanStatus sets the scan state fields for a repo.
func (r *Registry) UpdateScanStatus(id string, status ScanStatus, riskLevel, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.ScanStatus = status
	e.ScanError = errMsg
	if riskLevel != "" {
		e.RiskLevel = riskLevel
	}
	if status == ScanDone {
		e.ScannedAt = time.Now()
	}
	return r.save()
}

// UpdateMetadata sets description and language fields populated during indexing.
func (r *Registry) UpdateMetadata(id, description, language string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	if description != "" {
		e.Description = description
	}
	if language != "" {
		e.Language = language
	}
	return r.save()
}

// SetMemoryFile records the relative path to the repo's memory JSON file.
func (r *Registry) SetMemoryFile(id, relPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.MemoryFile = relPath
	return r.save()
}

// SetScanFile records the relative path to the repo's scan result JSON file.
func (r *Registry) SetScanFile(id, relPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.ScanFile = relPath
	return r.save()
}

// SetRefMDFile records the relative path to the human-readable reference markdown.
func (r *Registry) SetRefMDFile(id, relPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.RefMDFile = relPath
	return r.save()
}

// SetSummaryMDFile records the relative path to the LLM-compact summary markdown.
func (r *Registry) SetSummaryMDFile(id, relPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.SummaryMDFile = relPath
	return r.save()
}

// SetCallGraphFile records the relative path to the repo's call graph JSON file.
func (r *Registry) SetCallGraphFile(id, relPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	e.CallGraphFile = relPath
	return r.save()
}

// AddUser adds or replaces a user on the repo.
func (r *Registry) AddUser(id string, u RepoUser) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	if u.AddedAt.IsZero() {
		u.AddedAt = time.Now()
	}
	// Replace if handle already exists.
	for i, existing := range e.Users {
		if existing.Handle == u.Handle {
			e.Users[i] = u
			return r.save()
		}
	}
	e.Users = append(e.Users, u)
	return r.save()
}

// RemoveUser removes a user from the repo by handle.
func (r *Registry) RemoveUser(id, handle string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[id]
	if !ok {
		return fmt.Errorf("repointel: repo %q not found", id)
	}
	filtered := e.Users[:0]
	for _, u := range e.Users {
		if u.Handle != handle {
			filtered = append(filtered, u)
		}
	}
	if len(filtered) == len(e.Users) {
		return fmt.Errorf("repointel: user %q not found on repo %s", handle, id)
	}
	e.Users = filtered
	return r.save()
}

// ── Persistence ───────────────────────────────────────────────────────────────

// load reads the YAML file into r.data. Must be called with mu held or before
// the Registry is shared.
func (r *Registry) load() error {
	b, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	var f registryFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("repointel: parse registry: %w", err)
	}
	r.data = make(map[string]*RepoEntry, len(f.Repos))
	for _, e := range f.Repos {
		if e != nil {
			r.data[e.ID] = e
		}
	}
	return nil
}

// save serialises r.data to the YAML file atomically. Must be called with mu
// write-locked.
func (r *Registry) save() error {
	entries := make([]*RepoEntry, 0, len(r.data))
	for _, e := range r.data {
		entries = append(entries, e)
	}
	sortPtrByAddedAt(entries)

	f := registryFile{Repos: entries}
	b, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("repointel: marshal registry: %w", err)
	}

	// Atomic write: temp file → rename.
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func sortByAddedAt(s []RepoEntry) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].AddedAt.Before(s[j-1].AddedAt); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sortPtrByAddedAt(s []*RepoEntry) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].AddedAt.Before(s[j-1].AddedAt); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
