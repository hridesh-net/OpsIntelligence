package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
	"go.uber.org/zap"
)

// runTraceResponse is the JSON body for GET /api/v1/runtrace.
type runTraceResponse struct {
	Which     string            `json:"which"`
	Path      string            `json:"path"`
	Paths     []string          `json:"paths,omitempty"`
	Lines     []json.RawMessage `json:"lines"`
	Truncated bool              `json:"truncated"`
	ByteStart int64             `json:"byte_start,omitempty"`
}

func (s *AuthService) handleRunTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermRunTraceRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}

	which := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("which")))
	if which == "" {
		which = "master"
	}

	maxLines := 400
	if v := strings.TrimSpace(r.URL.Query().Get("max_lines")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			maxLines = n
		}
	}

	sources := s.discoverTraceSources(which)
	if len(sources) == 0 {
		writeJSONError(w, http.StatusNotFound, "run trace not configured for this stream")
		return
	}

	lines, truncated, paths, err := mergeRunTraceSources(sources, maxLines)
	if err != nil {
		s.Log.Warn("run trace merge", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "run trace read failed")
		return
	}
	pathLabel := strings.Join(paths, " · ")
	if pathLabel == "" {
		pathLabel = "(empty)"
	}
	writeJSON(w, http.StatusOK, runTraceResponse{
		Which:     which,
		Path:      pathLabel,
		Paths:     paths,
		Lines:     lines,
		Truncated: truncated,
	})
}

// traceSource pairs an absolute runtrace.ndjson path with the fallback
// `_stream` label used when the JSON line itself does not carry a runner_role.
type traceSource struct {
	path  string
	label string
}

// discoverTraceSources walks the configured agent / sub-agent log subtrees
// and returns every runtrace.ndjson file relevant to the requested stream.
// Falls back to the configured master/sub trace paths when the discovery
// roots are not set (older deployments / tests).
func (s *AuthService) discoverTraceSources(which string) []traceSource {
	masterAbs := filepath.Clean(strings.TrimSpace(s.RunTraceMaster))
	subAbs := strings.TrimSpace(s.RunTraceSubagent)
	if subAbs != "" {
		subAbs = filepath.Clean(subAbs)
	}

	var sources []traceSource
	seen := map[string]bool{}
	add := func(path, label string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		sources = append(sources, traceSource{path: path, label: label})
	}

	switch which {
	case "master":
		if masterAbs != "" {
			add(masterAbs, "master")
		}
		appendDiscovered(&sources, seen, s.LogsAgentDir, "master")
	case "subagent", "sub":
		if subAbs != "" {
			add(subAbs, "subagents")
		}
		appendDiscovered(&sources, seen, s.LogsSubagentsDir, "subagents")
	case "all":
		if masterAbs != "" {
			add(masterAbs, "master")
		}
		if subAbs != "" && subAbs != masterAbs {
			add(subAbs, "subagents")
		}
		appendDiscovered(&sources, seen, s.LogsAgentDir, "master")
		appendDiscovered(&sources, seen, s.LogsSubagentsDir, "subagents")
	}
	return sources
}

// appendDiscovered walks rootDir for runtrace.ndjson files and appends them
// to sources with a label derived from their path relative to rootDir.
func appendDiscovered(sources *[]traceSource, seen map[string]bool, rootDir, rootLabel string) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return
	}
	_ = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != "runtrace.ndjson" {
			return nil
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, path)
		label := traceLabelFromRel(rootLabel, rel)
		seen[clean] = true
		*sources = append(*sources, traceSource{path: clean, label: label})
		return nil
	})
}

// traceLabelFromRel turns a runtrace.ndjson path relative to its root log dir
// into a short stream label, e.g. "subagents/pr_review/abcd1234/runtrace.ndjson"
// → "sub:pr_review:abcd1234". Used as the _stream fallback when the JSON line
// does not already carry a runner_role.
func traceLabelFromRel(rootLabel, rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return rootLabel
	}
	if len(parts) == 1 && parts[0] == "runtrace.ndjson" {
		return rootLabel
	}
	trimmed := parts
	if trimmed[len(trimmed)-1] == "runtrace.ndjson" {
		trimmed = trimmed[:len(trimmed)-1]
	}
	prefix := "sub"
	if rootLabel == "master" {
		prefix = "agent"
	}
	short := func(s string) string {
		if len(s) > 14 {
			return s[:14]
		}
		return s
	}
	switch len(trimmed) {
	case 0:
		return rootLabel
	case 1:
		return prefix + ":" + short(trimmed[0])
	default:
		return prefix + ":" + short(trimmed[0]) + ":" + short(trimmed[len(trimmed)-1])
	}
}

// mergeRunTraceSources reads the tail of every source, tags each line with
// _stream (existing runner_role wins, otherwise the source's label), merges
// them by timestamp, and returns the most recent maxLines.
func mergeRunTraceSources(sources []traceSource, maxLines int) (lines []json.RawMessage, truncated bool, paths []string, err error) {
	if maxLines <= 0 {
		maxLines = 400
	}
	if len(sources) == 0 {
		return nil, false, nil, fmt.Errorf("no trace sources")
	}

	// Even split per source so a single noisy stream cannot starve others.
	per := maxLines / len(sources)
	if per < 32 {
		per = 32
	}

	type item struct {
		ts  time.Time
		raw json.RawMessage
	}
	var items []item
	paths = make([]string, 0, len(sources))
	for _, src := range sources {
		raw, trunc, _, rerr := readRunTraceTail(src.path, per)
		if rerr != nil {
			return nil, false, nil, rerr
		}
		if trunc {
			truncated = true
		}
		paths = append(paths, src.path)
		for _, ln := range raw {
			tagged := tagRunTraceLine(ln, src.label)
			items = append(items, item{ts: runTraceLineTime(tagged), raw: tagged})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ts.Before(items[j].ts) })
	if len(items) > maxLines {
		items = items[len(items)-maxLines:]
		truncated = true
	}
	out := make([]json.RawMessage, len(items))
	for i := range items {
		out[i] = items[i].raw
	}
	return out, truncated, paths, nil
}

func runTraceLineTime(raw json.RawMessage) time.Time {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return time.Time{}
	}
	s, _ := m["t"].(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if tt, err := time.Parse(layout, s); err == nil {
			return tt
		}
	}
	return time.Time{}
}

func inferRunnerStream(m map[string]any) string {
	if s, ok := m["runner_role"].(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	// Zap-style lines and some events carry session_id but not runner_role.
	if s, ok := m["session_id"].(string); ok {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "subagent:") || strings.HasPrefix(s, "cron:") {
			return s
		}
	}
	return ""
}

// tagRunTraceLine adds _stream for dashboard filtering (runner_role when set, else fileFallback).
func tagRunTraceLine(raw json.RawMessage, fileFallback string) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	stream := inferRunnerStream(m)
	if stream == "" {
		stream = fileFallback
	}
	m["_stream"] = stream
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return json.RawMessage(b)
}

func readRunTraceTail(absPath string, maxLines int) (lines []json.RawMessage, truncated bool, byteStart int64, err error) {
	f, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []json.RawMessage{}, false, 0, nil
		}
		return nil, false, 0, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, false, 0, err
	}
	size := st.Size()
	if size == 0 {
		return []json.RawMessage{}, false, 0, nil
	}

	const maxChunk = 512 * 1024
	byteStart = 0
	if size > maxChunk {
		byteStart = size - maxChunk
		truncated = true
		if _, err := f.Seek(byteStart, io.SeekStart); err != nil {
			return nil, false, 0, err
		}
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, false, 0, err
	}
	text := string(buf)
	if truncated && byteStart > 0 {
		if nl := strings.Index(text, "\n"); nl >= 0 {
			text = text[nl+1:]
		}
	}
	rawLines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(rawLines) > maxLines {
		rawLines = rawLines[len(rawLines)-maxLines:]
		truncated = true
	}
	out := make([]json.RawMessage, 0, len(rawLines))
	for _, ln := range rawLines {
		ln = strings.TrimSpace(ln)
		if ln == "" || !json.Valid([]byte(ln)) {
			continue
		}
		out = append(out, json.RawMessage(ln))
	}
	return out, truncated, byteStart, nil
}
