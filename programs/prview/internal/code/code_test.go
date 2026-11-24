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
		{"export const getUser = async (id: string) => {", "getUser"},
		{"export default async function getUser(id) {", "getUser"},
		{"const user = await getUser(id)", "user"},
		{"void Process(Frame f) {", "Process"},
		{"  virtual void Process(Frame f) override;", "Process"},
		{"func (s *Server) Handle(w http.ResponseWriter) {", "Handle"},
		{"func Map[T any](xs []T) []T {", "Map"},
		{"void Node::Run() {", "Run"},
		{"const std::string& name() const {", "name"},
		{"static constexpr int kMax = 5;", "kMax"},
		{"public static void main(String[] args) {", "main"},
		{"public async Task<User> GetUser(int id) {", "GetUser"},
		{"  public getUser(id: string): User {", "getUser"},
		{"  private cache: Map<string, User>;", "cache"},
		{"pub(crate) fn backoff(n: u32) -> u32 {", "backoff"},
		{"let mut seen = HashSet::new();", "seen"},
		{"async def fetch(url):", "fetch"},
		{"#define MAX(a, b) ((a) > (b) ? (a) : (b))", "MAX"},
		{"Foo::Foo(int x) : x_(x) {", "Foo"},
		{"EncodeScores(const std::vector<Score>& scores) {", "EncodeScores"},
		{"export const enum Color {", "Color"},
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
		// The name has to be the thing defined, not appear later on the line.
		{"const user = await getUser(id)", "getUser"},
		{"  let x = getUser(id);", "getUser"},
		{"void Process(Frame f) {", "Frame"},
		{"func (s *Server) Handle(w http.ResponseWriter) {", "Server"},
		{"func (s *Server) Handle(w http.ResponseWriter) {", "ResponseWriter"},
		{"class OcrTool : public RoiTool {", "RoiTool"},
		{"class Store extends Base {", "Base"},
		{"public User getUser() {", "User"},
		{"type Handler func(w http.ResponseWriter)", "ResponseWriter"},
		{"def fetch(url: Url) -> Page:", "Page"},
		// Statements and calls are uses.
		{"	go run(ctx)", "run"},
		{"	for x in items(y):", "items"},
		{"	if ready(x):", "ready"},
		{"  user: getUser(id),", "getUser"},
		{"	x, err := Parse(b)", "err"},
		{"	XRun(x)", "Run"},
		{"Run(x);", "Run"},
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
		{"a.cpp", " *", true},
		{"a.cpp", " */", true},
		{"a.cpp", "	*out = Build(cfg);", false},
		{"a.go", "	*p = Apply(x)", false},
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
