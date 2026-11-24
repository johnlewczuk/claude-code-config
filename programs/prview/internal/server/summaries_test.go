package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSummarizer counts calls and records prompts.
type fakeSummarizer struct {
	calls   atomic.Int32
	mu      sync.Mutex
	prompts []string
	gate    chan struct{} // when set, each call waits for it to close
}

func (f *fakeSummarizer) fn(ctx context.Context, system, prompt string) (string, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	first, _, _ := strings.Cut(prompt, "\n")
	return "sum of " + first, nil
}

var summaryFiles = map[string]string{
	"a.go":   "package a\n\nfunc Run() {}\n",
	"b.go":   "package a\n\nfunc Other() {\n\tRun()\n}\n",
	"go.sum": "x v1 h1:abc\n",
}

func newSummaryFixture(t *testing.T, base map[string]string, fs *fakeSummarizer) *refsFixture {
	t.Helper()
	head := map[string]string{}
	for p, c := range summaryFiles {
		head[p] = c
	}
	for p := range base {
		if _, ok := head[p]; !ok {
			head[p] = "" // deleted by the PR
		}
	}
	f := newRefsFixture(t, base, head)
	f.s.summarize = fs.fn
	st := f.s.prs["o/r/1"]
	files, err := st.repo.Diff(context.Background(), f.base, f.head)
	if err != nil {
		t.Fatal(err)
	}
	st.files = files
	return f
}

func getSummaries(t *testing.T, f *refsFixture) summariesPayload {
	t.Helper()
	w := do(f.h, "GET", "/api/pr/o/r/1/summaries", "", true, "")
	var p summariesPayload
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
	return p
}

func waitSummaries(t *testing.T, f *refsFixture, done func(summariesPayload) bool) summariesPayload {
	t.Helper()
	for i := 0; i < 500; i++ {
		if p := getSummaries(t, f); done(p) {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("summaries never settled: %+v", getSummaries(t, f))
	return summariesPayload{}
}

func allSettled(p summariesPayload) bool {
	if len(p.Files) == 0 {
		return false
	}
	for _, v := range p.Files {
		if v.Status != "ready" && v.Status != "skipped" {
			return false
		}
	}
	return true
}

func TestFileSummariesRun(t *testing.T) {
	fs := &fakeSummarizer{}
	f := newSummaryFixture(t, map[string]string{"README": "x\n"}, fs)
	st := f.s.prs["o/r/1"]
	f.s.startSummaries(st)
	p := waitSummaries(t, f, allSettled)
	if p.Files["a.go"].Status != "ready" || !strings.HasPrefix(p.Files["a.go"].Text, "sum of ") {
		t.Errorf("a.go: %+v", p.Files["a.go"])
	}
	if p.Files["b.go"].Status != "ready" || p.Files["go.sum"].Status != "skipped" || p.Files["go.sum"].Text == "" {
		t.Errorf("got %+v %+v", p.Files["b.go"], p.Files["go.sum"])
	}
	if n := fs.calls.Load(); n != 3 {
		t.Errorf("%d calls, want 3", n)
	}
	f.s.startSummaries(st) // the same head again
	waitSummaries(t, f, allSettled)
	if n := fs.calls.Load(); n != 3 {
		t.Errorf("%d calls after a second load, want 3", n)
	}
}

func TestFileSummariesStaleRun(t *testing.T) {
	fs := &fakeSummarizer{gate: make(chan struct{})}
	f := newSummaryFixture(t, map[string]string{"README": "x\n"}, fs)
	st := f.s.prs["o/r/1"]
	f.s.startSummaries(st)
	waitSummaries(t, f, func(p summariesPayload) bool { return fs.calls.Load() >= 2 })
	st.mu.Lock()
	st.sumGen++ // a reload replaced the run
	st.sums = map[string]*fileSum{"a.go": {Status: "waiting"}}
	st.mu.Unlock()
	close(fs.gate)
	time.Sleep(200 * time.Millisecond)
	if v := getSummaries(t, f).Files["a.go"]; v.Status != "waiting" {
		t.Errorf("a stale result was kept: %+v", v)
	}
}

func TestSummariesOff(t *testing.T) {
	f := newSummaryFixture(t, map[string]string{"README": "x\n"}, &fakeSummarizer{})
	f.s.summarize = nil
	f.s.startSummaries(f.s.prs["o/r/1"])
	p := getSummaries(t, f)
	if p.Off == "" || len(p.Files) != 0 {
		t.Errorf("got %+v", p)
	}
}

func postSection(f *refsFixture, body string) (int, string) {
	w := do(f.h, "POST", "/api/pr/o/r/1/summary/section", body, true, own)
	return w.Code, w.Body.String()
}

func TestSectionSummary(t *testing.T) {
	fs := &fakeSummarizer{}
	f := newSummaryFixture(t, map[string]string{"README": "x\n"}, fs)
	body := `{"path":"a.go","side":"new","start":3,"end":3,"name":"Run"}`
	code, out := postSection(f, body)
	if code != 200 || !strings.Contains(out, "sum of Explain Run") {
		t.Fatalf("got %d %s", code, out)
	}
	p := fs.prompts[0]
	if !strings.Contains(p, "+func Run() {}") || !strings.Contains(p, "Used in:") || !strings.Contains(p, "b.go:4:") {
		t.Errorf("prompt: %s", p)
	}
	postSection(f, body)
	if n := fs.calls.Load(); n != 1 {
		t.Errorf("%d calls, want 1", n)
	}
}

func TestSectionsOld(t *testing.T) {
	f := newSummaryFixture(t, map[string]string{"old.go": "package a\n\nfunc Gone() {\n}\n"}, &fakeSummarizer{})
	w := do(f.h, "GET", "/api/pr/o/r/1/sections?path=old.go&side=old", "", true, "")
	var out struct{ Sections []struct{ Name string } }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	found := false
	for _, s := range out.Sections {
		found = found || s.Name == "Gone"
	}
	if !found {
		t.Errorf("got %s", w.Body)
	}
}
