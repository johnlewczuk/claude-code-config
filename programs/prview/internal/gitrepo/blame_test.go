package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

// testRepo builds a small repo and returns a Repo pointing at it.
type testRepo struct {
	t   *testing.T
	dir string
}

func newTestRepo(t *testing.T) *testRepo {
	dir := t.TempDir()
	tr := &testRepo{t, dir}
	tr.run("init", "-q", "-b", "main")
	tr.run("config", "user.email", "t@example.com")
	tr.run("config", "user.name", "t")
	return tr
}

func (tr *testRepo) run(args ...string) string {
	tr.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", tr.dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tr.t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (tr *testRepo) commit(msg string, files map[string]string) string {
	tr.t.Helper()
	for p, c := range files {
		full := filepath.Join(tr.dir, p)
		if c == "" {
			os.Remove(full)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			tr.t.Fatal(err)
		}
	}
	tr.run("add", "-A")
	tr.run("commit", "-q", "-m", msg)
	return tr.run("rev-parse", "HEAD")
}

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

// commitsOf returns "kind:number=commit" for each changed line.
func commitsOf(files []diff.File, names map[string]string) []string {
	var out []string
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.Added:
					out = append(out, "+"+l.Text+"="+names[l.Commit])
				case diff.Removed:
					out = append(out, "-"+l.Text+"="+names[l.Commit])
				}
			}
		}
	}
	return out
}

func TestAttribute(t *testing.T) {
	tr := newTestRepo(t)
	base := tr.commit("base", map[string]string{"a.txt": lines("one", "two", "three", "four")})
	tr.run("checkout", "-q", "-b", "pr")
	c1 := tr.commit("c1: add five, drop two", map[string]string{"a.txt": lines("one", "three", "four", "five")})
	c2 := tr.commit("c2: change four, drop three", map[string]string{"a.txt": lines("one", "FOUR", "five")})
	c3 := tr.commit("c3: change FOUR again", map[string]string{"a.txt": lines("one", "FOUR!", "five")})
	names := map[string]string{c1: "c1", c2: "c2", c3: "c3", "": "-", base: "base"}

	r := &Repo{Dir: tr.dir}
	ctx := context.Background()
	files, err := r.Diff(ctx, base, c3)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Attribute(ctx, base, c3, files); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(commitsOf(files, names), " ")
	// "two" was removed by c1, "three" and "four" by c2. "FOUR!" was added by
	// c3, "five" by c1.
	want := "-two=c1 -three=c2 -four=c2 +FOUR!=c3 +five=c1"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestAttributeWithBaseMergedIn(t *testing.T) {
	tr := newTestRepo(t)
	tr.commit("base", map[string]string{"a.txt": lines("one", "two"), "b.txt": lines("b1")})
	tr.run("checkout", "-q", "-b", "pr")
	c1 := tr.commit("c1", map[string]string{"a.txt": lines("one", "two", "pr-line")})
	tr.run("checkout", "-q", "main")
	m2 := tr.commit("main moves on", map[string]string{"b.txt": lines("b1", "main-line")})
	tr.run("checkout", "-q", "pr")
	tr.run("merge", "-q", "--no-edit", "main")
	merge := tr.run("rev-parse", "HEAD")
	c2 := tr.commit("c2", map[string]string{"b.txt": lines("b1", "main-line", "pr-b")})

	r := &Repo{Dir: tr.dir}
	ctx := context.Background()
	mb, err := r.MergeBase(ctx, "main", c2)
	if err != nil {
		t.Fatal(err)
	}
	if mb != m2 {
		t.Fatalf("merge base %s, want main's tip", mb)
	}
	files, err := r.Diff(ctx, mb, c2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Attribute(ctx, mb, c2, files); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{c1: "c1", c2: "c2", merge: "merge", "": "-"}
	got := strings.Join(commitsOf(files, names), " ")
	// main-line came from main, so it isn't in the diff at all; the PR's
	// own lines are credited to the PR's commits, not the merge.
	want := "+pr-line=c1 +pr-b=c2"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
