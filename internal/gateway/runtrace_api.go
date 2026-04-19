package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
	"go.uber.org/zap"
)

// runTraceResponse is the JSON body for GET /api/v1/runtrace.
type runTraceResponse struct {
	Which     string            `json:"which"`
	Path      string            `json:"path"`
	Lines     []json.RawMessage `json:"lines"`
	Truncated bool              `json:"truncated"`
	ByteStart int64             `json:"byte_start"`
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
	var wantPath string
	switch which {
	case "master":
		wantPath = s.RunTraceMaster
	case "subagent", "sub":
		wantPath = s.RunTraceSubagent
	default:
		writeJSONError(w, http.StatusBadRequest, "which must be master or subagent")
		return
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
	masterAbs := filepath.Clean(strings.TrimSpace(s.RunTraceMaster))
	subAbs := strings.TrimSpace(s.RunTraceSubagent)
	var subClean string
	if subAbs != "" {
		subClean = filepath.Clean(subAbs)
	}
	allowed := abs == masterAbs
	if subClean != "" {
		allowed = allowed || abs == subClean
	}
	if !allowed {
		writeJSONError(w, http.StatusForbidden, "invalid trace selection")
		return
	}

	maxLines := 400
	if v := strings.TrimSpace(r.URL.Query().Get("max_lines")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			maxLines = n
		}
	}

	lines, truncated, start, err := readRunTraceTail(abs, maxLines)
	if err != nil {
		s.Log.Warn("run trace read", zap.String("path", abs), zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "run trace read failed")
		return
	}
	writeJSON(w, http.StatusOK, runTraceResponse{
		Which:     which,
		Path:      abs,
		Lines:     lines,
		Truncated: truncated,
		ByteStart: start,
	})
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
