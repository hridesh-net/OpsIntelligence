package repointel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// CallNode is a function, method, or class extracted from source code.
type CallNode struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Kind    string `json:"kind"` // "function" | "method" | "class" | "module" | "file"
	Package string `json:"package,omitempty"`
}

// CallEdge is a directed relationship in the graph.
type CallEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // "call" | "import"
}

// CallGraph is the full function call network extracted from a repo.
type CallGraph struct {
	RepoID    string     `json:"repo_id"`
	CreatedAt time.Time  `json:"created_at"`
	Nodes     []CallNode `json:"nodes"`
	Edges     []CallEdge `json:"edges"`
}

// ── Graph queries ─────────────────────────────────────────────────────────────

// Callees returns the IDs of nodes that nodeID calls.
func (g *CallGraph) Callees(nodeID string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.From == nodeID {
			out = append(out, e.To)
		}
	}
	return out
}

// Callers returns the IDs of nodes that call nodeID.
func (g *CallGraph) Callers(nodeID string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.To == nodeID {
			out = append(out, e.From)
		}
	}
	return out
}

// NodeByID returns the node with the given ID, or zero value.
func (g *CallGraph) NodeByID(id string) (CallNode, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return CallNode{}, false
}

// ── Builder ───────────────────────────────────────────────────────────────────

type funcSpan struct {
	name   string
	nodeID string
	start  int // line number (1-based)
	end    int // exclusive; 0 = unknown (uses next span start)
	body   string
}

// BuildCallGraph analyses raw files and constructs a call graph.
// It uses regex-based, language-aware parsing — approximate but fast.
func BuildCallGraph(repoID string, files []RawFile) *CallGraph {
	cg := &CallGraph{
		RepoID:    repoID,
		CreatedAt: time.Now().UTC(),
	}

	// Maps for dedup and lookup.
	nodeIndex := map[string]CallNode{} // id → node
	byName := map[string][]string{}    // name → []id
	edgeSet := map[string]struct{}{}   // "from→to"

	// Pass 1: extract all function definitions.
	var spans []funcSpan // all spans across all files, for body extraction
	for _, f := range files {
		lang := langFromPath(f.Path)
		if lang == "" {
			continue
		}
		pkg := packageName(lang, f.Content)
		defs := extractDefs(lang, repoID, f.Path, pkg, f.Content)
		for _, def := range defs {
			nodeIndex[def.nodeID] = CallNode{
				ID:      def.nodeID,
				Name:    def.name,
				File:    f.Path,
				Line:    def.start,
				Kind:    "function",
				Package: pkg,
			}
			byName[def.name] = append(byName[def.name], def.nodeID)
		}
		spans = append(spans, defs...)
	}

	// Populate Nodes (sorted for determinism).
	cg.Nodes = make([]CallNode, 0, len(nodeIndex))
	for _, n := range nodeIndex {
		cg.Nodes = append(cg.Nodes, n)
	}
	sort.Slice(cg.Nodes, func(i, j int) bool {
		if cg.Nodes[i].File != cg.Nodes[j].File {
			return cg.Nodes[i].File < cg.Nodes[j].File
		}
		return cg.Nodes[i].Line < cg.Nodes[j].Line
	})

	// Pass 2: for each span's body, find calls to known functions.
	for _, sp := range spans {
		if sp.body == "" {
			continue
		}
		callerID := sp.nodeID
		for name, calleeIDs := range byName {
			if name == sp.name {
				continue // skip self
			}
			if containsCall(sp.body, name) {
				for _, calleeID := range calleeIDs {
					key := callerID + "→" + calleeID
					if _, dup := edgeSet[key]; !dup {
						edgeSet[key] = struct{}{}
						cg.Edges = append(cg.Edges, CallEdge{From: callerID, To: calleeID, Kind: "call"})
					}
				}
			}
		}
	}

	// Pass 3: lightweight import / module edges (same-repo files → external modules).
	appendImportEdges(cg, files, edgeSet)

	sort.Slice(cg.Nodes, func(i, j int) bool {
		if cg.Nodes[i].Kind != cg.Nodes[j].Kind {
			return cg.Nodes[i].Kind < cg.Nodes[j].Kind
		}
		if cg.Nodes[i].File != cg.Nodes[j].File {
			return cg.Nodes[i].File < cg.Nodes[j].File
		}
		return cg.Nodes[i].Line < cg.Nodes[j].Line
	})

	// Stable edge sort (kind, from, to).
	sort.Slice(cg.Edges, func(i, j int) bool {
		if cg.Edges[i].Kind != cg.Edges[j].Kind {
			return cg.Edges[i].Kind < cg.Edges[j].Kind
		}
		if cg.Edges[i].From != cg.Edges[j].From {
			return cg.Edges[i].From < cg.Edges[j].From
		}
		return cg.Edges[i].To < cg.Edges[j].To
	})

	return cg
}

// appendImportEdges adds module nodes and import edges from the first
// function in each file to resolved import paths (best-effort, regex-based).
func appendImportEdges(cg *CallGraph, files []RawFile, edgeSet map[string]struct{}) {
	nodeSeen := make(map[string]struct{}, len(cg.Nodes))
	for _, n := range cg.Nodes {
		nodeSeen[n.ID] = struct{}{}
	}
	for _, f := range files {
		lang := langFromPath(f.Path)
		if lang == "" {
			continue
		}
		mods := extractImportPaths(lang, f.Content)
		if len(mods) == 0 {
			continue
		}
		fromID := firstFuncNodeIDInFile(cg.Nodes, f.Path)
		if fromID == "" {
			// Import-only files (no extracted functions): anchor imports on a file node.
			fromID = makeFileNodeID(cg.RepoID, f.Path)
			if _, ok := nodeSeen[fromID]; !ok {
				nodeSeen[fromID] = struct{}{}
				cg.Nodes = append(cg.Nodes, CallNode{
					ID:   fromID,
					Name: filepath.Base(f.Path),
					File: f.Path,
					Line: 0,
					Kind: "file",
				})
			}
		}
		for _, mod := range mods {
			mod = strings.TrimSpace(mod)
			if mod == "" || strings.HasPrefix(mod, ".") {
				continue
			}
			modID := makeModuleNodeID(cg.RepoID, mod)
			if _, ok := nodeSeen[modID]; !ok {
				nodeSeen[modID] = struct{}{}
				cg.Nodes = append(cg.Nodes, CallNode{
					ID:   modID,
					Name: shortModuleLabel(mod),
					File: mod,
					Line: 0,
					Kind: "module",
				})
			}
			key := fromID + "→" + modID + ":import"
			if _, dup := edgeSet[key]; dup {
				continue
			}
			edgeSet[key] = struct{}{}
			cg.Edges = append(cg.Edges, CallEdge{From: fromID, To: modID, Kind: "import"})
		}
	}
}

func firstFuncNodeIDInFile(nodes []CallNode, filePath string) string {
	for _, n := range nodes {
		if n.File == filePath && n.Kind != "module" && n.Kind != "file" && n.Line > 0 {
			return n.ID
		}
	}
	return ""
}

func makeFileNodeID(repoID, filePath string) string {
	safe := strings.NewReplacer("/", "-", ".", "-", " ", "_").Replace(filePath)
	return fmt.Sprintf("%s::file::%s", repoID, safe)
}

func makeModuleNodeID(repoID, mod string) string {
	safe := strings.NewReplacer("/", "--", ".", "-", " ", "_", "(", "", ")", "", "@", "-at-").Replace(mod)
	return fmt.Sprintf("%s::mod::%s", repoID, safe)
}

func shortModuleLabel(mod string) string {
	if i := strings.LastIndex(mod, "/"); i >= 0 && i+1 < len(mod) {
		return mod[i+1:]
	}
	return mod
}

var (
	goImportSingleRe    = regexp.MustCompile(`^\s*import\s+(?:\w+\s+)?"([^"]+)"`)
	goImportQuotedPaths = regexp.MustCompile(`"([^"]+)"`)
	jsFromRe            = regexp.MustCompile(`(?m)(?:^|\s)from\s+['"]([^'"]+)['"]`)
	jsRequireRe         = regexp.MustCompile(`(?m)require\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	pyFromImportRe      = regexp.MustCompile(`(?m)^\s*from\s+([\w.]+)\s+import`)
	pyImportRe          = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+)`)
)

func extractImportPaths(lang, content string) []string {
	switch lang {
	case "go":
		return extractGoImports(content)
	case "javascript", "typescript":
		return extractJSImport(content)
	case "python":
		return extractPythonImports(content)
	default:
		return nil
	}
}

func extractGoImports(content string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	lines := strings.Split(content, "\n")
	inBlock := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "import (") {
			inBlock = true
			continue
		}
		if inBlock {
			if trim == ")" {
				inBlock = false
				continue
			}
			// "_ "pkg" or `pkg` or alias "path"
			if strings.Contains(line, `"`) {
				for _, m := range goImportQuotedPaths.FindAllStringSubmatch(line, -1) {
					if len(m) > 1 {
						add(m[1])
					}
				}
			}
			continue
		}
		if m := goImportSingleRe.FindStringSubmatch(line); len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

func extractJSImport(content string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, m := range jsFromRe.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	for _, m := range jsRequireRe.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

func extractPythonImports(content string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, m := range pyFromImportRe.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	for _, m := range pyImportRe.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			add(m[1])
		}
	}
	return out
}

// ── Language detection ────────────────────────────────────────────────────────

func langFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".c":
		return "c"
	}
	return ""
}

// ── Package name extraction ───────────────────────────────────────────────────

func packageName(lang, content string) string {
	var re *regexp.Regexp
	switch lang {
	case "go":
		re = regexp.MustCompile(`(?m)^package\s+(\w+)`)
	case "python":
		// Python uses directory modules; use first class name as hint.
		re = regexp.MustCompile(`(?m)^class\s+(\w+)`)
	case "javascript", "typescript":
		// Use file-level module detection is unreliable; skip.
		return ""
	case "java":
		re = regexp.MustCompile(`(?m)^package\s+([\w.]+)`)
	case "rust":
		re = regexp.MustCompile(`(?m)^mod\s+(\w+)`)
	default:
		return ""
	}
	if m := re.FindStringSubmatch(content); len(m) > 1 {
		return m[1]
	}
	return ""
}

// ── Definition extractors (per language) ─────────────────────────────────────

// extractDefs returns function spans for the given language + file content.
func extractDefs(lang, repoID, filePath, pkg, content string) []funcSpan {
	lines := strings.Split(content, "\n")
	switch lang {
	case "go":
		return extractGoFuncs(repoID, filePath, pkg, lines)
	case "python":
		return extractPythonFuncs(repoID, filePath, lines)
	case "javascript", "typescript":
		return extractJSFuncs(repoID, filePath, lines)
	case "java":
		return extractJavaFuncs(repoID, filePath, pkg, lines)
	case "rust":
		return extractRustFuncs(repoID, filePath, pkg, lines)
	case "ruby":
		return extractRubyFuncs(repoID, filePath, lines)
	default:
		return nil
	}
}

// makeNodeID builds a deterministic node ID for a function.
func makeNodeID(repoID, filePath, name string, line int) string {
	safe := strings.NewReplacer("/", "-", ".", "-", " ", "_", "(", "", ")", "").Replace
	return fmt.Sprintf("%s::%s::%s::%d", repoID, safe(filePath), safe(name), line)
}

// ── Go ────────────────────────────────────────────────────────────────────────

var goFuncRe = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?(\w+)\s*\(`)

func extractGoFuncs(repoID, filePath, pkg string, lines []string) []funcSpan {
	var out []funcSpan
	for i, line := range lines {
		m := goFuncRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		lineNo := i + 1
		id := makeNodeID(repoID, filePath, name, lineNo)
		body := extractBraceBody(lines, i)
		out = append(out, funcSpan{name: name, nodeID: id, start: lineNo, body: body})
	}
	return out
}

// ── Python ────────────────────────────────────────────────────────────────────

var pyFuncRe = regexp.MustCompile(`^(\s*)def\s+(\w+)\s*\(`)

func extractPythonFuncs(repoID, filePath string, lines []string) []funcSpan {
	var out []funcSpan
	for i, line := range lines {
		m := pyFuncRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent := len(m[1])
		name := m[2]
		lineNo := i + 1
		id := makeNodeID(repoID, filePath, name, lineNo)
		body := extractIndentBody(lines, i, indent)
		out = append(out, funcSpan{name: name, nodeID: id, start: lineNo, body: body})
	}
	return out
}

// ── JavaScript / TypeScript ───────────────────────────────────────────────────

var (
	jsFuncDeclRe  = regexp.MustCompile(`(?:^|\s)function\s+(\w+)\s*\(`)
	jsArrowRe     = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?\(`)
	jsMethodRe    = regexp.MustCompile(`^\s+(\w+)\s*\([^)]*\)\s*\{`)
	jsClassFuncRe = regexp.MustCompile(`^\s*(?:async\s+)?(\w+)\s*\(`)
)

func extractJSFuncs(repoID, filePath string, lines []string) []funcSpan {
	var out []funcSpan
	for i, line := range lines {
		var name string
		if m := jsFuncDeclRe.FindStringSubmatch(line); m != nil {
			name = m[1]
		} else if m := jsArrowRe.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
		if name == "" || isJSKeyword(name) {
			continue
		}
		lineNo := i + 1
		id := makeNodeID(repoID, filePath, name, lineNo)
		body := extractBraceBody(lines, i)
		out = append(out, funcSpan{name: name, nodeID: id, start: lineNo, body: body})
	}
	return out
}

func isJSKeyword(s string) bool {
	switch s {
	case "if", "for", "while", "switch", "catch", "else", "return", "new",
		"class", "extends", "import", "export", "default", "from", "async",
		"await", "typeof", "instanceof", "delete", "void", "throw", "in":
		return true
	}
	return false
}

// ── Java ──────────────────────────────────────────────────────────────────────

var javaMethodRe = regexp.MustCompile(
	`(?:public|private|protected|static|final|synchronized|abstract|native)[\s\w<>\[\]]*\s+(\w+)\s*\(`)

func extractJavaFuncs(repoID, filePath, pkg string, lines []string) []funcSpan {
	var out []funcSpan
	for i, line := range lines {
		m := javaMethodRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if isJavaKeyword(name) {
			continue
		}
		lineNo := i + 1
		id := makeNodeID(repoID, filePath, name, lineNo)
		body := extractBraceBody(lines, i)
		out = append(out, funcSpan{name: name, nodeID: id, start: lineNo, body: body})
	}
	return out
}

func isJavaKeyword(s string) bool {
	switch s {
	case "if", "for", "while", "switch", "catch", "return", "new", "class",
		"interface", "void", "boolean", "int", "long", "double", "float",
		"String", "Object", "Override", "throws", "throw", "try":
		return true
	}
	return false
}

// ── Rust ──────────────────────────────────────────────────────────────────────

var rustFnRe = regexp.MustCompile(`(?:pub\s+)?(?:async\s+)?fn\s+(\w+)\s*(?:<[^>]*>)?\s*\(`)

func extractRustFuncs(repoID, filePath, pkg string, lines []string) []funcSpan {
	var out []funcSpan
	for i, line := range lines {
		m := rustFnRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		lineNo := i + 1
		id := makeNodeID(repoID, filePath, name, lineNo)
		body := extractBraceBody(lines, i)
		out = append(out, funcSpan{name: name, nodeID: id, start: lineNo, body: body})
	}
	return out
}

// ── Ruby ──────────────────────────────────────────────────────────────────────

var rubyDefRe = regexp.MustCompile(`^\s*def\s+(self\.)?(\w+)`)

func extractRubyFuncs(repoID, filePath string, lines []string) []funcSpan {
	var out []funcSpan
	for i, line := range lines {
		m := rubyDefRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[2]
		lineNo := i + 1
		id := makeNodeID(repoID, filePath, name, lineNo)
		body := extractRubyBody(lines, i)
		out = append(out, funcSpan{name: name, nodeID: id, start: lineNo, body: body})
	}
	return out
}

// ── Body extraction helpers ───────────────────────────────────────────────────

// extractBraceBody scans from the opening line, counting braces until balanced.
// Returns up to 200 lines of body text for call-site analysis.
func extractBraceBody(lines []string, startIdx int) string {
	depth := 0
	var body strings.Builder
	for i := startIdx; i < len(lines) && i < startIdx+200; i++ {
		line := lines[i]
		body.WriteString(line + "\n")
		for _, ch := range line {
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 && i > startIdx {
					return body.String()
				}
			}
		}
	}
	return body.String()
}

// extractIndentBody collects lines more indented than the def line (Python).
func extractIndentBody(lines []string, startIdx, defIndent int) string {
	var body strings.Builder
	for i := startIdx + 1; i < len(lines) && i < startIdx+200; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			body.WriteString(line + "\n")
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= defIndent {
			break
		}
		body.WriteString(line + "\n")
	}
	return body.String()
}

// extractRubyBody collects lines until an 'end' at the same depth.
func extractRubyBody(lines []string, startIdx int) string {
	depth := 1
	var body strings.Builder
	for i := startIdx + 1; i < len(lines) && i < startIdx+200; i++ {
		line := lines[i]
		body.WriteString(line + "\n")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "do") ||
			strings.HasPrefix(trimmed, "if ") || strings.HasPrefix(trimmed, "while ") ||
			strings.HasPrefix(trimmed, "begin") || strings.HasPrefix(trimmed, "class ") {
			depth++
		} else if trimmed == "end" {
			depth--
			if depth == 0 {
				return body.String()
			}
		}
	}
	return body.String()
}

// ── Call-site detection ───────────────────────────────────────────────────────

// callRe matches name( patterns (function calls).
var callRe = regexp.MustCompile(`\b(\w+)\s*\(`)

// containsCall returns true if body contains a call to the named function.
func containsCall(body, name string) bool {
	// Quick pre-check before regex.
	if !strings.Contains(body, name) {
		return false
	}
	all := callRe.FindAllStringSubmatch(body, -1)
	for _, m := range all {
		if m[1] == name {
			return true
		}
	}
	return false
}

// ── Persistence ───────────────────────────────────────────────────────────────

// SaveCallGraph writes g to path as formatted JSON.
func SaveCallGraph(path string, g *CallGraph) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadCallGraph reads a CallGraph from path.
func LoadCallGraph(path string) (*CallGraph, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g CallGraph
	return &g, json.Unmarshal(b, &g)
}

// CallGraphPath returns the standard path for a repo's call graph file.
func CallGraphPath(memoryDir, repoID string) string {
	return filepath.Join(memoryDir, sanitiseID(repoID)+"-callgraph.json")
}

// ── Web export ────────────────────────────────────────────────────────────────

// ExportGraphHTML writes a self-contained HTML file with an interactive 2D/3D
// force-directed visualization of the call graph using vis.js.
func ExportGraphHTML(path string, g *CallGraph) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Encode nodes/edges as JSON for embedding.
	type visNode struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Title string `json:"title"`
		Group string `json:"group"`
	}
	type visEdgeExt struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Arrow  string `json:"arrows"`
		Dashes bool   `json:"dashes,omitempty"`
	}

	visNodes := make([]visNode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		var group, title string
		switch n.Kind {
		case "module":
			group = "module"
			title = fmt.Sprintf("%s\n%s", n.Name, n.File)
		case "file":
			group = "file"
			title = fmt.Sprintf("%s\n%s", n.Name, n.File)
		default:
			group = filepath.Dir(n.File)
			if group == "." {
				group = n.File
			}
			title = fmt.Sprintf("%s\n%s:%d", n.Name, n.File, n.Line)
		}
		visNodes = append(visNodes, visNode{ID: n.ID, Label: n.Name, Title: title, Group: group})
	}

	visEdges := make([]visEdgeExt, 0, len(g.Edges))
	for _, e := range g.Edges {
		visEdges = append(visEdges, visEdgeExt{
			From: e.From, To: e.To, Arrow: "to", Dashes: e.Kind == "import",
		})
	}

	nodesJSON, _ := json.Marshal(visNodes)
	edgesJSON, _ := json.Marshal(visEdges)

	html := fmt.Sprintf(graphHTMLTemplate,
		g.RepoID,
		g.RepoID,
		len(g.Nodes),
		len(g.Edges),
		g.CreatedAt.Format("2006-01-02 15:04 UTC"),
		string(nodesJSON),
		string(edgesJSON),
	)
	return os.WriteFile(path, []byte(html), 0o644)
}

const graphHTMLTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Call Graph — %s</title>
<script src="https://unpkg.com/vis-network@9.1.9/standalone/umd/vis-network.min.js"></script>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { background: #0d1117; color: #c9d1d9; font-family: ui-monospace, monospace; }
  #header { padding: 12px 20px; background: #161b22; border-bottom: 1px solid #30363d; display: flex; align-items: center; gap: 16px; }
  #header h1 { font-size: 15px; color: #58a6ff; }
  #header span { font-size: 12px; color: #8b949e; }
  #search { padding: 5px 10px; background: #21262d; border: 1px solid #30363d; border-radius: 6px; color: #c9d1d9; font-size: 13px; width: 220px; }
  #network { width: 100%%; height: calc(100vh - 52px); }
  #tooltip { position: fixed; background: #161b22; border: 1px solid #30363d; border-radius: 6px; padding: 8px 12px; font-size: 12px; display: none; pointer-events: none; white-space: pre; max-width: 360px; }
</style>
</head>
<body>
<div id="header">
  <h1>%s — Call Graph</h1>
  <span>%d nodes · %d edges · %s</span>
  <input id="search" type="text" placeholder="Find function…" oninput="filterNodes(this.value)">
</div>
<div id="network"></div>
<div id="tooltip"></div>
<script>
const rawNodes = %s;
const rawEdges = %s;

// Assign colors by group (directory).
const groupColors = {};
const palette = ['#58a6ff','#3fb950','#d2a8ff','#ffa657','#f78166','#79c0ff','#56d364','#bc8cff'];
let gi = 0;
function groupColor(g) {
  if (!groupColors[g]) { groupColors[g] = palette[gi++ %% palette.length]; }
  return groupColors[g];
}

const nodes = new vis.DataSet(rawNodes.map(n => ({
  id: n.id,
  label: n.label,
  title: n.title,
  color: { background: groupColor(n.group), border: '#30363d', highlight: { background: '#f0f6fc', border: '#58a6ff' } },
  font: { color: '#f0f6fc', size: 11 },
  shape: 'box',
  margin: 6,
})));

const edges = new vis.DataSet(rawEdges.map(e => ({
  from: e.from, to: e.to,
  arrows: e.arrows,
  dashes: !!e.dashes,
  color: { color: e.dashes ? '#6e7681' : '#484f58', highlight: '#58a6ff' },
  smooth: { type: 'curvedCW', roundness: 0.15 },
})));

const container = document.getElementById('network');
const data = { nodes, edges };
const options = {
  physics: {
    solver: 'forceAtlas2Based',
    forceAtlas2Based: { gravitationalConstant: -60, springLength: 120, springConstant: 0.08 },
    stabilization: { iterations: 200 },
  },
  interaction: { hover: true, tooltipDelay: 150, navigationButtons: true, keyboard: true },
  layout: { improvedLayout: true },
};
const network = new vis.Network(container, data, options);

network.on('hoverNode', params => {
  const node = nodes.get(params.node);
  const tip = document.getElementById('tooltip');
  tip.textContent = node.title;
  tip.style.display = 'block';
  tip.style.left = params.event.center.x + 12 + 'px';
  tip.style.top = params.event.center.y + 12 + 'px';
});
network.on('blurNode', () => { document.getElementById('tooltip').style.display = 'none'; });

function filterNodes(q) {
  q = q.toLowerCase();
  nodes.update(rawNodes.map(n => ({
    id: n.id,
    hidden: q !== '' && !n.label.toLowerCase().includes(q),
  })));
}
</script>
</body>
</html>
`
