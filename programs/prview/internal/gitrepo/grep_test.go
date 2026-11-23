package gitrepo

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestGrep(t *testing.T) {
	tr := newTestRepo(t)
	head := tr.commit("one", map[string]string{
		"a.go":    lines("func Run() {}", "x := Run()", "y := RunAll()"),
		"b/c.cpp": lines("void Node::Run() {", "}"),
		"bin.dat": "Run\x00\x01",
	})
	r := &Repo{Dir: tr.dir}
	ms, more, err := r.Grep(context.Background(), head, "Run", 100)
	if err != nil || more != 0 {
		t.Fatal(err, more)
	}
	var got []string
	for _, m := range ms {
		got = append(got, fmt.Sprintf("%s:%d:%d %s", m.Path, m.Line, m.Col, m.Text))
	}
	want := []string{"a.go:1:6 func Run() {}", "a.go:2:6 x := Run()", "b/c.cpp:1:12 void Node::Run() {"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v", got)
	}
}

func TestGrepCap(t *testing.T) {
	tr := newTestRepo(t)
	var ls []string
	for i := 0; i < 50; i++ {
		ls = append(ls, "err = f()")
	}
	head := tr.commit("one", map[string]string{"a.go": lines(ls...)})
	r := &Repo{Dir: tr.dir}
	ms, more, err := r.Grep(context.Background(), head, "err", 30)
	if err != nil || len(ms) != 30 || more != 20 {
		t.Fatalf("got %d, more %d, err %v", len(ms), more, err)
	}
}

func TestGrepLiteral(t *testing.T) {
	tr := newTestRepo(t)
	head := tr.commit("one", map[string]string{"a.cpp": lines("a.b(x)", "axb(x)", "$v = 1")})
	r := &Repo{Dir: tr.dir}
	ms, _, _ := r.Grep(context.Background(), head, "a.b", 10)
	if len(ms) != 1 || ms[0].Line != 1 {
		t.Fatalf("a.b: %+v", ms)
	}
	ms, _, _ = r.Grep(context.Background(), head, "$v", 10)
	if len(ms) != 1 {
		t.Fatalf("$v: %+v", ms)
	}
}
