package gateway

import (
	"encoding/json"
	"fmt"
	"io"
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

	masterAbs := filepath.Clean(strings.TrimSpace(s.RunTraceMaster))
	subRaw := strings.TrimSpace(s.RunTraceSubagent)
	var subClean string
	if subRaw != "" {
		subClean = filepath.Clean(subRaw)
	}

	switch which {
	case "all":
		if masterAbs == "" && subClean == "" {
			writeJSONError(w, http.StatusNotFound, "run trace not configured for this stream")
			return
		}
		lines, truncated, paths, err := mergeRunTrace(masterAbs, subClean, maxLines)
		if err != nil {
			s.Log.Warn("run trace merge", zap.Error(err))
			writeJSONError(w, http.StatusInternalServerError, "run trace read failed")
			return
		}
		pathLabel := strings.Join(paths, " + ")
		if pathLabel == "" {
			pathLabel = "(merged)"
		}
		writeJSON(w, http.StatusOK, runTraceResponse{
			Which:     "all",
			Path:      pathLabel,
			Paths:     paths,
			Lines:     lines,
			Truncated: truncated,
		})
		return
	case "master", "subagent", "sub":
		// continue below
	default:
		writeJSONError(w, http.StatusBadRequest, "which must be master, subagent, or all")
		return
	}

	wantPath := ""
	switch which {
	case "master":
		wantPath = s.RunTraceMaster
	case "subagent", "sub":
		wantPath = s.RunTraceSubagent
	}
	if wantPath == "" {
		writeJSONError(w, http.StatusNotFound, "run trace not configured for this stream")
		return
	}
	abs := filepath.Clean(wantPath)
	if !filepath.IsAbs(abs) {
		writeJSONError(w, http.StatusInternalServerError, "invalid trace path")
		return
	}
	allowed := abs == masterAbs
	if subClean != "" {
		allowed = allowed || abs == subClean
	}
	if !allowed {
		writeJSONError(w, http.StatusForbidden, "invalid trace selection")
		return
	}

	lines, truncated, start, err := readRunTraceTail(abs, maxLines)
	if err != nil {
		s.Log.Warn("run trace read", zap.String("path", abs), zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "run trace read failed")
		return
	}
	fileTag := "master"
	if which == "subagent" || which == "sub" {
		fileTag = "subagent"
	}
	for i := range lines {
		lines[i] = tagRunTraceLine(lines[i], fileTag)
	}
	writeJSON(w, http.StatusOK, runTraceResponse{
		Which:     which,
		Path:      abs,
		Lines:     lines,
		Truncated: truncated,
		ByteStart: start,
	})
}

func mergeRunTrace(masterAbs, subClean string, maxLines int) (lines []json.RawMessage, truncated bool, paths []string, err error) {
	if maxLines <= 0 {
		maxLines = 400
	}
	// Single path (or duplicate config pointing at the same file).
	if subClean == "" || masterAbs == subClean {
		if masterAbs == "" {
			return nil, false, nil, fmt.Errorf("no trace path")
		}
		lm, trunc, _, err := readRunTraceTail(masterAbs, maxLines)
		if err != nil {
			return nil, false, nil, err
		}
		for i := range lm {
			lm[i] = tagRunTraceLine(lm[i], "master")
		}
		return lm, trunc, []string{masterAbs}, nil
	}
	if masterAbs == "" {
		ls, trunc, _, err := readRunTraceTail(subClean, maxLines)
		if err != nil {
			return nil, false, nil, err
		}
		for i := range ls {
			ls[i] = tagRunTraceLine(ls[i], "subagent")
		}
		return ls, trunc, []string{subClean}, nil
	}

	lm, truncM, _, err := readRunTraceTail(masterAbs, maxLines)
	if err != nil {
		return nil, false, nil, err
	}
	ls, truncS, _, err := readRunTraceTail(subClean, maxLines)
	if err != nil {
		return nil, false, nil, err
	}

	type item struct {
		ts  time.Time
		raw json.RawMessage
	}
	items := make([]item, 0, len(lm)+len(ls))
	for _, raw := range lm {
		items = append(items, item{ts: runTraceLineTime(raw), raw: tagRunTraceLine(raw, "master")})
	}
	for _, raw := range ls {
		items = append(items, item{ts: runTraceLineTime(raw), raw: tagRunTraceLine(raw, "subagent")})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ts.Before(items[j].ts)
	})
	if len(items) > maxLines {
		items = items[len(items)-maxLines:]
		truncated = true
	} else {
		truncated = truncM || truncS || len(lm)+len(ls) > maxLines
	}
	out := make([]json.RawMessage, len(items))
	for i := range items {
		out[i] = items[i].raw
	}
	return out, truncated, []string{masterAbs, subClean}, nil
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
		return strings.TrimSpace(s)
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
