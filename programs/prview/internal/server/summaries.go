package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/code"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/summary"
)

const (
	summaryWorkers        = 4 // file summaries written at once in the background
	askWorkers            = 2 // section summaries and redone file summaries asked at once
	fileSummaryTimeout    = 2 * time.Minute
	sectionSummaryTimeout = 60 * time.Second
	sectionUses           = 20
	// sectionUsesWait is how long a section summary waits for the list of
	// places its name is used; usesLookupLimit is how long that lookup may
	// carry on after the summary stopped waiting for it.
	sectionUsesWait = 5 * time.Second
	usesLookupLimit = 2 * time.Minute
)

// fileSum is where one file's summary stands.
type fileSum struct {
	Status string `json:"status"` // waiting, running, ready, failed or skipped
	Text   string `json:"text"`   // the summary, or the reason it was skipped
	Error  string `json:"error"`
}

type summariesPayload struct {
	Off   string              `json:"off"` // why there are no summaries, if there are none
	Files map[string]*fileSum `json:"files"`
}

// setSummarize makes summaries ask Claude (sonnet, no tools) through the
// server's runner; with no runner there are no summaries.
func (s *Server) setSummarize() {
	if s.claude == nil {
		return
	}
	runner := s.claude.WithModel("sonnet")
	s.summarize = func(ctx context.Context, system, prompt string) (string, error) {
		var out struct {
			Summary string `json:"summary"`
		}
		if err := runner.JSON(ctx, os.TempDir(), system, prompt, summary.Schema, &out); err != nil {
			return "", err
		}
		return strings.TrimSpace(out.Summary), nil
	}
}

// startSummaries starts writing a summary for every file of the loaded PR,
// four at a time, in the background. A load of the same diff keeps the run
// there is; a load of a new one replaces it.
func (s *Server) startSummaries(st *prState) {
	if s.summarize == nil {
		return
	}
	st.mu.Lock()
	pr, base, head, files := st.pr, st.base, st.head, st.files
	if pr == nil || st.sumDiff == base+".."+head {
		st.mu.Unlock()
		return
	}
	st.sumDiff = base + ".." + head
	st.sumGen++
	gen := st.sumGen
	st.sums = make(map[string]*fileSum, len(files))
	for _, f := range files {
		st.sums[f.Path] = &fileSum{Status: "waiting"}
	}
	st.mu.Unlock()

	queue := make(chan diff.File)
	for i := 0; i < summaryWorkers; i++ {
		go func() {
			for f := range queue {
				s.summarizeFile(st, gen, pr.Title, pr.Body, base, head, f, false)
			}
		}()
	}
	go func() {
		defer close(queue)
		for _, f := range files {
			queue <- f
		}
	}()
}

// ask runs one summary the user asked for, at most askWorkers at a time.
func (s *Server) ask(ctx context.Context, system, prompt string) (string, error) {
	select {
	case s.asks <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.asks }()
	return s.summarize(ctx, system, prompt)
}

// setSum records one file's state, unless a newer run has replaced this one.
func (st *prState) setSum(gen int, path string, v fileSum) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.sumGen == gen && st.sums[path] != nil {
		*st.sums[path] = v
	}
}

func (st *prState) currentGen(gen int) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sumGen == gen
}

// summarizeFile writes one file's summary. redo asks Claude even when a
// saved copy exists, and waits for a free place among the asks.
func (s *Server) summarizeFile(st *prState, gen int, title, body, base, head string, f diff.File, redo bool) {
	if reason := summary.Skip(f); reason != "" {
		st.setSum(gen, f.Path, fileSum{Status: "skipped", Text: reason})
		return
	}
	key := summary.FileKey(f)
	if text, ok := s.store.Get("files", key); ok && !redo {
		st.setSum(gen, f.Path, fileSum{Status: "ready", Text: text})
		return
	}
	if !st.currentGen(gen) {
		return
	}
	st.setSum(gen, f.Path, fileSum{Status: "running"})

	ctx, cancel := context.WithTimeout(context.Background(), fileSummaryTimeout)
	defer cancel()
	rev, path := head, f.Path
	if f.Status == "deleted" {
		rev, path = base, f.OldPath
	}
	text, err := st.repo.FileLines(ctx, rev, path)
	if err != nil {
		text = nil // the diff alone still says enough
	}
	call := s.summarize
	if redo {
		call = s.ask
	}
	out, err := call(ctx, summary.FileSystem, summary.FilePrompt(title, body, f, text))
	if err == nil && out == "" {
		err = errors.New("claude sent back an empty summary")
	}
	if err != nil {
		log.Printf("summary of %s in %s/%s#%d: %v", f.Path, st.owner, st.name, st.number, err)
		st.setSum(gen, f.Path, fileSum{Status: "failed", Error: err.Error()})
		return
	}
	if err := s.store.Put("files", key, out); err != nil {
		log.Printf("saving the summary of %s: %v", f.Path, err)
	}
	st.setSum(gen, f.Path, fileSum{Status: "ready", Text: out})
}

// fileSums returns a copy of where each file's summary stands.
func (st *prState) fileSums() map[string]*fileSum {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make(map[string]*fileSum, len(st.sums))
	for path, v := range st.sums {
		c := *v
		out[path] = &c
	}
	return out
}

func (s *Server) summariesPayload(st *prState) summariesPayload {
	if s.summarize == nil {
		return summariesPayload{Off: "the claude command isn't installed, so there are no summaries", Files: map[string]*fileSum{}}
	}
	return summariesPayload{Files: st.fileSums()}
}

func (s *Server) handleSummaries(w http.ResponseWriter, r *http.Request, st *prState) {
	writeJSON(w, s.summariesPayload(st))
}

// handleSummariesRedo asks again for one file, replacing its saved copy.
func (s *Server) handleSummariesRedo(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if s.summarize == nil {
		writeJSON(w, s.summariesPayload(st))
		return
	}
	st.mu.Lock()
	var file *diff.File
	for i := range st.files {
		if st.files[i].Path == req.Path {
			file = &st.files[i]
		}
	}
	if file == nil || st.pr == nil {
		st.mu.Unlock()
		httpError(w, http.StatusNotFound, fmt.Errorf("%s isn't a file of this PR", req.Path))
		return
	}
	f, pr, base, head, gen := *file, st.pr, st.base, st.head, st.sumGen
	// A file still waiting gets its summary from the background run.
	if v := st.sums[f.Path]; v == nil || v.Status == "waiting" || v.Status == "running" || v.Status == "skipped" {
		st.mu.Unlock()
		writeJSON(w, s.summariesPayload(st))
		return
	}
	st.sums[f.Path] = &fileSum{Status: "waiting"}
	st.mu.Unlock()

	go s.summarizeFile(st, gen, pr.Title, pr.Body, base, head, f, true)
	writeJSON(w, s.summariesPayload(st))
}

// revFor returns the commit a side of the diff reads from.
func (st *prState) revFor(side string) (string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	rev := st.head
	if side == "old" {
		rev = st.base
	}
	if rev == "" {
		return "", errors.New("this PR isn't loaded; reload the page")
	}
	return rev, nil
}

func (s *Server) handleSections(w http.ResponseWriter, r *http.Request, st *prState) {
	path, side := r.URL.Query().Get("path"), r.URL.Query().Get("side")
	rev, err := st.revFor(side)
	if err != nil {
		httpError(w, http.StatusConflict, err)
		return
	}
	lines, err := st.repo.FileLines(r.Context(), rev, path)
	if err != nil {
		httpError(w, http.StatusNotFound, err)
		return
	}
	secs := code.Sections(path, lines)
	if secs == nil {
		secs = []code.Section{}
	}
	writeJSON(w, map[string]any{"sections": secs})
}

type sectionRequest struct {
	Path  string `json:"path"`
	Side  string `json:"side"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Name  string `json:"name"`
}

func (s *Server) handleSectionSummary(w http.ResponseWriter, r *http.Request, st *prState) {
	var req sectionRequest
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if s.summarize == nil {
		httpError(w, http.StatusServiceUnavailable, errors.New("the claude command isn't installed, so there are no summaries"))
		return
	}
	rev, err := st.revFor(req.Side)
	if err != nil {
		httpError(w, http.StatusConflict, err)
		return
	}
	text, err := st.repo.FileLines(r.Context(), rev, req.Path)
	if err != nil {
		httpError(w, http.StatusNotFound, err)
		return
	}
	if req.Start < 1 || req.End < req.Start || req.Start > len(text) {
		httpError(w, http.StatusBadRequest, errors.New("those lines aren't in the file"))
		return
	}

	st.mu.Lock()
	var file *diff.File
	for i := range st.files {
		f := &st.files[i]
		if (req.Side == "old" && f.OldPath == req.Path) || (req.Side != "old" && f.Path == req.Path) {
			c := *f
			file = &c
		}
	}
	fileSummary := ""
	if v := st.sums[req.Path]; v != nil && v.Status == "ready" {
		fileSummary = v.Text
	}
	st.mu.Unlock()

	in := summary.SectionInput{
		Path: req.Path, Name: req.Name, FileSummary: fileSummary,
		Lines: summary.MarkSection(file, req.Side, text, req.Start, req.End),
	}
	key := summary.SectionKey(req.Path, in.Lines)
	if out, ok := s.store.Get("sections", key); ok {
		writeJSON(w, map[string]string{"summary": out})
		return
	}
	if req.Name != "" {
		in.Uses = s.sectionUses(r, st, req, text)
	}
	ctx, cancel := context.WithTimeout(r.Context(), sectionSummaryTimeout)
	defer cancel()
	out, err := s.ask(ctx, summary.SectionSystem, summary.SectionPrompt(in))
	if err == nil && out == "" {
		err = errors.New("claude sent back an empty summary")
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	if err := s.store.Put("sections", key, out); err != nil {
		log.Printf("saving a section summary: %v", err)
	}
	writeJSON(w, map[string]string{"summary": out})
}

// sectionUses lists where the section's name is used, the way Ctrl+click
// does. It waits at most sectionUsesWait; past that, or when the lookup
// fails, there are no uses. A lookup still running then carries on in the
// background, so gopls or the downloaded files are ready next time.
func (s *Server) sectionUses(r *http.Request, st *prState, req sectionRequest, text []string) []code.Place {
	q := refsQuery{Path: req.Path, Side: req.Side, Name: req.Name}
	for n := req.Start; n <= req.End && n <= len(text) && q.Line == 0; n++ {
		if name, col := code.DefinedName(text[n-1]); name == req.Name {
			q.Line, q.Col = n, col
		}
	}
	if q.Line == 0 {
		return nil
	}
	found := make(chan []code.Place, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), usesLookupLimit)
		defer cancel()
		p, err := s.refs(ctx, r.WithContext(ctx), st, q, func(string) {})
		if err != nil || p == nil {
			found <- nil
			return
		}
		places := p.Places
		if len(places) > sectionUses {
			places = places[:sectionUses]
		}
		found <- places
	}()
	select {
	case places := <-found:
		return places
	case <-time.After(sectionUsesWait):
		return nil
	case <-r.Context().Done():
		return nil
	}
}
