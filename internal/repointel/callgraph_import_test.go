package repointel

import (
	"strings"
	"testing"
)

func TestBuildCallGraph_goImportsAndCalls(t *testing.T) {
	files := []RawFile{{
		Path: "main.go",
		Content: `package main

import "fmt"
import "github.com/foo/bar"

func main() {
	fmt.Println("hi")
	bar.Baz()
}
`,
	}}
	g := BuildCallGraph("github:acme/demo", files)
	if len(g.Nodes) < 2 {
		t.Fatalf("expected at least main + module nodes, got %d", len(g.Nodes))
	}
	var importEdges int
	for _, e := range g.Edges {
		if e.Kind == "import" {
			importEdges++
		}
	}
	if importEdges == 0 {
		t.Fatal("expected at least one import edge")
	}
	hasMod := false
	for _, n := range g.Nodes {
		if n.Kind == "module" && strings.Contains(n.File, "github.com/foo/bar") {
			hasMod = true
			break
		}
	}
	if !hasMod {
		t.Fatal("expected module node for github.com/foo/bar")
	}
}

func TestBuildCallGraph_importOnlyGoFile(t *testing.T) {
	// No functions — only package + imports; graph should still get file + module nodes.
	files := []RawFile{{
		Path: "doc.go",
		Content: `// Package x
package x

import "encoding/json"
`,
	}}
	g := BuildCallGraph("github:acme/pkg", files)
	if len(g.Nodes) < 2 {
		t.Fatalf("expected at least file + module nodes, got %d: %+v", len(g.Nodes), g.Nodes)
	}
	var fileN, modN int
	for _, n := range g.Nodes {
		switch n.Kind {
		case "file":
			fileN++
		case "module":
			modN++
		}
	}
	if fileN < 1 || modN < 1 {
		t.Fatalf("file=%d module=%d nodes=%+v", fileN, modN, g.Nodes)
	}
}

func TestExtractJSImport(t *testing.T) {
	src := `import x from './local.js';
import { a } from "npm-pkg";
const y = require('other');
`
	out := extractJSImport(src)
	if len(out) < 2 {
		t.Fatalf("imports: %+v", out)
	}
}
