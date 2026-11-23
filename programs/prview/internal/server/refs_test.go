package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/gitrepo"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/gopls"
)

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitFiles writes files (an empty string removes one), commits them and
// returns the new commit.
func commitFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if c == "" {
			os.Remove(full)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "c")
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// refsFixture is a PR "o/r#1" over a real repo: a base commit and a head
// commit, with b.go the one changed file.
type refsFixture struct {
	s    *Server
	h    http.Handler
	dir  string
	base string
	head string
}

func newRefsFixture(t *testing.T, base, head map[string]string) *refsFixture {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "t@example.com")
	gitIn(t, dir, "config", "user.name", "t")
	b := commitFiles(t, dir, base)
	hd := commitFiles(t, dir, head)
	s, _, h := newTestServer(t)
	st := s.prs["o/r/1"]
	st.repo = &gitrepo.Repo{Dir: dir}
	st.base, st.head = b, hd
	st.files = []diff.File{{Path: "b.go", OldPath: "b.go"}}
	s.gopls = nil // tests that want gopls set a fake
	s.checkout = func(*http.Request, *prState) (string, error) { return dir, nil }
	return &refsFixture{s: s, h: h, dir: dir, base: b, head: hd}
}

// refs calls /refs and returns the decoded ready event, or the error of a
// failed one.
func (f *refsFixture) refs(t *testing.T, query string) (*refsPayload, string) {
	t.Helper()
	w := do(f.h, "GET", "/api/pr/o/r/1/refs?"+query, "", true, "")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	var event string
	sc := bufio.NewScanner(w.Body)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			switch event {
			case "ready":
				var p refsPayload
				if err := json.Unmarshal([]byte(data), &p); err != nil {
					t.Fatal(err)
				}
				return &p, ""
			case "failed":
				var e struct{ Error string }
				json.Unmarshal([]byte(data), &e)
				return nil, e.Error
			}
		}
	}
	t.Fatalf("no ready or failed event in %q", w.Body)
	return nil, ""
}

func placeList(p *refsPayload) string {
	var out []string
	for _, pl := range p.Places {
		s := pl.Kind + " " + pl.Path + ":" + itoa(pl.Line)
		if pl.Changed {
			s += " changed"
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

var runFiles = map[string]string{"a.go": "func Run() {}\n", "b.go": "\tRun()\n"}

func TestRefsByName(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"x.txt": "x\n"}, runFiles)
	p, e := f.refs(t, "path=b.go&side=new&line=1&col=2&name=Run")
	if p == nil {
		t.Fatal(e)
	}
	if p.Source != "name" || p.More != 0 {
		t.Errorf("source %q more %d", p.Source, p.More)
	}
	if got, want := placeList(p), "definition a.go:1; use b.go:1 changed"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRefsOldSide(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"old.go": "func Gone() {}\n"}, map[string]string{"old.go": "", "b.go": "x\n"})
	p, e := f.refs(t, "path=old.go&side=old&line=1&col=6&name=Gone")
	if p == nil {
		t.Fatal(e)
	}
	if got, want := placeList(p), "definition old.go:1"; got != want {
		t.Fatalf("old: got %q want %q", got, want)
	}
	p, e = f.refs(t, "path=b.go&side=new&line=1&col=1&name=Gone")
	if p == nil {
		t.Fatal(e)
	}
	if len(p.Places) != 0 {
		t.Fatalf("new: %q", placeList(p))
	}
}

func TestRefsTruncates(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"x.txt": "x\n"}, map[string]string{"a.go": strings.Repeat("err = f()\n", 350)})
	p, e := f.refs(t, "path=a.go&side=new&line=1&col=1&name=err")
	if p == nil {
		t.Fatal(e)
	}
	if len(p.Places) != 300 || p.More != 50 {
		t.Fatalf("places %d, more %d", len(p.Places), p.More)
	}
}

// The definition is in the file whose path sorts last, past any early cap on
// the search.
func TestRefsFindsDefinitionPastTheFirstMatches(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"x.txt": "x\n"}, map[string]string{
		"a.go":      strings.Repeat("\tRun()\n", 350),
		"zz/def.go": "func Run() {}\n",
	})
	p, e := f.refs(t, "path=a.go&side=new&line=1&col=2&name=Run")
	if p == nil {
		t.Fatal(e)
	}
	if len(p.Places) != 300 {
		t.Fatalf("places %d", len(p.Places))
	}
	if first := p.Places[0]; first.Kind != "definition" || first.Path != "zz/def.go" {
		t.Fatalf("first place: %+v", first)
	}
	if p.More != 51 {
		t.Errorf("more %d, want 51", p.More)
	}
}

func TestRefsNeedsLoadedPR(t *testing.T) {
	s, _, h := newTestServer(t)
	s.prs["o/r/1"].head = ""
	w := do(h, "GET", "/api/pr/o/r/1/refs?path=a.go&side=new&name=Run", "", true, "")
	if !strings.Contains(w.Body.String(), "event: failed") || !strings.Contains(w.Body.String(), "this PR isn't loaded; reload the page") {
		t.Fatalf("got %s", w.Body)
	}
}

// fakeGopls answers with fixed places, or an error.
type fakeGopls struct {
	defs, uses []gopls.Loc
	err        error
	calls      int
}

func (g *fakeGopls) Definition(ctx context.Context, path string, line, col int) ([]gopls.Loc, error) {
	g.calls++
	return g.defs, g.err
}
func (g *fakeGopls) References(ctx context.Context, path string, line, col int) ([]gopls.Loc, error) {
	g.calls++
	return g.uses, g.err
}
func (g *fakeGopls) Close() {}

func (f *refsFixture) useGopls(g *fakeGopls) {
	f.s.gopls = gopls.NewPool(func(context.Context, string) (gopls.Client, error) { return g, nil }, time.Minute, 2)
}

func TestRefsUsesGoplsForGo(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"x.txt": "x\n"}, runFiles)
	f.useGopls(&fakeGopls{
		defs: []gopls.Loc{{Path: "a.go", Line: 1, Col: 6}},
		uses: []gopls.Loc{{Path: "b.go", Line: 1, Col: 2}},
	})
	p, e := f.refs(t, "path=b.go&side=new&line=1&col=2&name=Run")
	if p == nil {
		t.Fatal(e)
	}
	if p.Source != "gopls" {
		t.Errorf("source %q", p.Source)
	}
	if got, want := placeList(p), "definition a.go:1; use b.go:1 changed"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if p.Places[0].Text != "func Run() {}" {
		t.Errorf("text %q", p.Places[0].Text)
	}
}

func TestRefsFallsBackWhenGoplsFails(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"x.txt": "x\n"}, runFiles)
	f.useGopls(&fakeGopls{err: errors.New("boom")})
	p, e := f.refs(t, "path=b.go&side=new&line=1&col=2&name=Run")
	if p == nil {
		t.Fatal(e)
	}
	if got, want := placeList(p), "definition a.go:1; use b.go:1 changed"; p.Source != "name" || got != want {
		t.Fatalf("source %q got %q want %q", p.Source, got, want)
	}
}

func TestRefsNameForOtherLanguages(t *testing.T) {
	f := newRefsFixture(t, map[string]string{"x.txt": "x\n"}, map[string]string{"b.go": "x\n", "x.cpp": "void Run() {\n}\n"})
	g := &fakeGopls{}
	f.useGopls(g)
	p, e := f.refs(t, "path=x.cpp&side=new&line=1&col=6&name=Run")
	if p == nil {
		t.Fatal(e)
	}
	if p.Source != "name" || g.calls != 0 {
		t.Errorf("source %q, gopls calls %d", p.Source, g.calls)
	}
}
