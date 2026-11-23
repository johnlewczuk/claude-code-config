package gopls

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteGoWork(t *testing.T) {
	root := t.TempDir()
	write(t, root, "services/a/go.mod", "module a\n")
	write(t, root, "services/b/go.mod", "module b\n")
	write(t, root, "vendor/x/go.mod", "module x\n")
	work := filepath.Join(t.TempDir(), "go.work")
	use, err := WriteGoWork(root, work)
	if err != nil || !use {
		t.Fatal(use, err)
	}
	b, _ := os.ReadFile(work)
	got := string(b)
	for _, want := range []string{filepath.Join(root, "services/a"), filepath.Join(root, "services/b")} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Contains(got, "vendor") {
		t.Errorf("vendor listed:\n%s", got)
	}

	// A checkout with its own go.work is left alone.
	own := t.TempDir()
	write(t, own, "go.work", "go 1.26\n")
	write(t, own, "a/go.mod", "module a\n")
	work2 := filepath.Join(t.TempDir(), "go.work")
	use, err = WriteGoWork(own, work2)
	if err != nil || use {
		t.Fatal(use, err)
	}
	if _, err := os.Stat(work2); err == nil {
		t.Error("wrote a go.work although the checkout has one")
	}
}

// fakeClient counts closes.
type fakeClient struct {
	mu     sync.Mutex
	closed bool
}

func (f *fakeClient) Definition(ctx context.Context, path string, line, col int) ([]Loc, error) {
	return nil, nil
}
func (f *fakeClient) References(ctx context.Context, path string, line, col int) ([]Loc, error) {
	return nil, nil
}
func (f *fakeClient) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }
func (f *fakeClient) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// fakeStarts hands out fakeClients and remembers them.
type fakeStarts struct {
	mu      sync.Mutex
	clients []*fakeClient
}

func (s *fakeStarts) start(ctx context.Context, dir string) (Client, error) {
	c := &fakeClient{}
	s.mu.Lock()
	s.clients = append(s.clients, c)
	s.mu.Unlock()
	return c, nil
}

func (s *fakeStarts) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func waitClosed(t *testing.T, c *fakeClient) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c.isClosed() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("client was not closed")
}

func TestPoolReuses(t *testing.T) {
	fs := &fakeStarts{}
	p := NewPool(fs.start, time.Minute, 3)
	defer p.Close()
	a, _ := p.Get(context.Background(), "o/r/1@h1", "/d")
	b, _ := p.Get(context.Background(), "o/r/1@h1", "/d")
	if a != b || fs.count() != 1 {
		t.Fatalf("starts: %d", fs.count())
	}
}

func TestPoolNewHeadRestarts(t *testing.T) {
	fs := &fakeStarts{}
	p := NewPool(fs.start, time.Minute, 3)
	defer p.Close()
	p.Get(context.Background(), "o/r/1@h1", "/d")
	p.Get(context.Background(), "o/r/1@h2", "/d")
	if fs.count() != 2 {
		t.Fatalf("starts: %d", fs.count())
	}
	waitClosed(t, fs.clients[0])
	if fs.clients[1].isClosed() {
		t.Error("the new client was closed")
	}
}

func TestPoolMax(t *testing.T) {
	fs := &fakeStarts{}
	p := NewPool(fs.start, time.Minute, 2)
	defer p.Close()
	for _, k := range []string{"k1", "k2", "k3"} {
		p.Get(context.Background(), k, "/d")
	}
	waitClosed(t, fs.clients[0])
	if fs.clients[1].isClosed() || fs.clients[2].isClosed() {
		t.Error("closed the wrong client")
	}
}

func TestPoolIdle(t *testing.T) {
	fs := &fakeStarts{}
	p := NewPool(fs.start, 50*time.Millisecond, 3)
	defer p.Close()
	p.Get(context.Background(), "k1", "/d")
	time.Sleep(150 * time.Millisecond)
	waitClosed(t, fs.clients[0])
}

func TestSessionAcrossModules(t *testing.T) {
	bin, err := exec.LookPath("gopls")
	if err != nil {
		t.Skip("gopls is not installed")
	}
	root := t.TempDir()
	write(t, root, "shared/go.mod", "module acusight/shared\n\ngo 1.21\n")
	write(t, root, "shared/s.go", "package shared\n\nfunc Apply() {}\n")
	write(t, root, "svc/go.mod", "module acusight/svc\n\ngo 1.21\n\nrequire acusight/shared v0.0.0\n\nreplace acusight/shared => ../shared\n")
	write(t, root, "svc/main.go", "package main\n\nimport \"acusight/shared\"\n\nfunc main() { shared.Apply() }\n")
	work := filepath.Join(t.TempDir(), "go.work")
	use, err := WriteGoWork(root, work)
	if err != nil || !use {
		t.Fatal(use, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := Start(ctx, bin, root, []string{"GOWORK=" + work})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	refs, err := s.References(ctx, "shared/s.go", 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range refs {
		if l.Path == "svc/main.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("references: %+v", refs)
	}
	defs, err := s.Definition(ctx, "svc/main.go", 5, 24)
	if err != nil || len(defs) != 1 || defs[0].Path != "shared/s.go" || defs[0].Line != 3 {
		t.Errorf("definition: %+v %v", defs, err)
	}
}
