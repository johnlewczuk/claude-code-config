package code

import (
	"fmt"
	"strings"
	"testing"
)

func TestIsDefinition(t *testing.T) {
	yes := []struct{ line, name string }{
		{"func ApplyLoginFromEnv(db *gorm.DB) error {", "ApplyLoginFromEnv"},
		{"func (q *Queue) Enqueue(j Job) error {", "Enqueue"},
		{"type Queue struct {", "Queue"},
		{"def backoff(n):", "backoff"},
		{"Chain CameraChain::Build(const Cfg& c)", "Build"},
		{"static int helper();", "helper"},
		{"export function stepTarget(lines: DLine[]) {", "stepTarget"},
		{"class OcrTool : public RoiTool {", "OcrTool"},
	}
	for _, c := range yes {
		if !IsDefinition(c.line, c.name) {
			t.Errorf("want definition: %q", c.line)
		}
	}
	no := []struct{ line, name string }{
		{"	if err := shareddb.ApplyLoginFromEnv(db, u, p); err != nil {", "ApplyLoginFromEnv"},
		{"  return helper();", "helper"},
		{"	q.Enqueue(job)", "Enqueue"},
	}
	for _, c := range no {
		if IsDefinition(c.line, c.name) {
			t.Errorf("want use: %q", c.line)
		}
	}
}

func TestIsCommentOnly(t *testing.T) {
	for _, c := range []struct {
		path, line string
		want       bool
	}{
		{"a.go", "	// Enqueue adds a job", true},
		{"a.go", "	q.Enqueue(j) // and here", false},
		{"a.py", "  # backoff is capped", true},
		{"a.cpp", " * Build makes the chain", true},
		{"a.cpp", "/* Build */", true},
		{"a.sql", "-- run Build", true},
		{"a.go", "x := 1", false},
	} {
		if got := IsCommentOnly(c.path, c.line); got != c.want {
			t.Errorf("%s %q: got %v", c.path, c.line, got)
		}
	}
}

func TestRank(t *testing.T) {
	ms := []Match{
		{Path: "z/other.go", Line: 9, Text: "	Run(x)"},
		{Path: "a/here.go", Line: 40, Text: "	Run(y)"},
		{Path: "a/near.go", Line: 3, Text: "	Run(z)"},
		{Path: "b/changed.go", Line: 7, Text: "	Run(w)"},
		{Path: "b/def.go", Line: 12, Text: "func Run(v int) {"},
		{Path: "c/node.cpp", Line: 5, Text: "void Node::Run() {"},
		{Path: "a/here.go", Line: 2, Text: "// Run does things"},
	}
	got := Rank("Run", "Node", "a/here.go", ms, map[string]bool{"b/changed.go": true})
	var order []string
	for _, p := range got {
		order = append(order, fmt.Sprintf("%s %s:%d %v", p.Kind, p.Path, p.Line, p.Changed))
	}
	want := []string{
		"definition c/node.cpp:5 false", // hint "Node" matches its qualifier
		"definition b/def.go:12 false",
		"use a/here.go:40 false",  // same file
		"use b/changed.go:7 true", // changed in the PR
		"use a/near.go:3 false",   // same folder
		"use z/other.go:9 false",
		"comment a/here.go:2 false",
	}
	if strings.Join(order, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
}
