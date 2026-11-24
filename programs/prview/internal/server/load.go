package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/github"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/gitrepo"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/order"
)

// prState is one open PR.
type prState struct {
	owner, name string
	number      int
	repo        *gitrepo.Repo

	mu    sync.Mutex
	pr    *github.PR
	base  string // merge-base the diff is against
	head  string // head commit the page was loaded at
	files []diff.File

	treeReady map[string]bool // revs whose files are all downloaded

	ord        *order.Order
	ordErr     string
	ordRunning bool
	ordGen     int // bumps on every reload/redo so stale runs are ignored

	sums   map[string]*fileSum // file summaries by path
	sumGen int                 // bumps on every reload so a stale run is ignored

	chatMu sync.Mutex // one question at a time per PR
}

// loadPayload is what the page gets once a PR is ready.
type loadPayload struct {
	Owner  string       `json:"owner"`
	Repo   string       `json:"repo"`
	PR     *github.PR   `json:"pr"`
	Base   string       `json:"base"`
	Head   string       `json:"head"`
	Files  []diff.File  `json:"files"`
	Order  orderPayload `json:"order"`
	Claude bool         `json:"claude"` // whether the Claude panel works
}

// handleLoad fetches everything for a PR, streaming progress, and ends with
// a "ready" event carrying the payload.
func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request, st *prState) {
	send := sse(w)
	progress := func(msg string) { send("progress", map[string]string{"message": msg}) }
	payload, err := s.load(r.Context(), st, progress)
	if err != nil {
		send("failed", map[string]string{"error": err.Error()})
		return
	}
	send("ready", payload)
}

func (s *Server) load(ctx context.Context, st *prState, progress func(string)) (*loadPayload, error) {
	progress("Reading the PR from GitHub")
	pr, err := s.gh.PullRequest(ctx, st.owner, st.name, st.number)
	if err != nil {
		return nil, err
	}
	var last time.Time
	err = st.repo.Fetch(ctx, st.number, pr.BaseRefName, func(line string) {
		// git sends many progress lines; pass on a few per second.
		if time.Since(last) > 200*time.Millisecond {
			last = time.Now()
			progress(line)
		}
	})
	if err != nil {
		return nil, err
	}
	head := pr.HeadRefOid
	if !st.repo.HasCommit(ctx, head) {
		return nil, fmt.Errorf("the PR head %s wasn't fetched; try again in a moment", head[:8])
	}
	base, err := st.repo.MergeBase(ctx, "refs/prview/base/"+pr.BaseRefName, head)
	if err != nil {
		return nil, err
	}
	progress("Getting the changed files")
	paths, err := st.repo.ChangedPaths(ctx, base, head)
	if err != nil {
		return nil, err
	}
	if err := st.repo.PrefetchBlobs(ctx, base, head, paths); err != nil {
		return nil, err
	}
	progress("Working out which commit changed each line")
	files, err := st.repo.Diff(ctx, base, head)
	if err != nil {
		return nil, err
	}
	if err := st.repo.Attribute(ctx, base, head, files); err != nil {
		return nil, err
	}

	st.mu.Lock()
	st.pr, st.base, st.head, st.files = pr, base, head, files
	if st.ord != nil && st.ord.Head != head {
		st.ord, st.ordErr = nil, ""
	}
	st.mu.Unlock()
	s.startOrder(st, false)
	s.startSummaries(st)

	return &loadPayload{Owner: st.owner, Repo: st.name, PR: pr, Base: base, Head: head, Files: files,
		Order: st.orderPayload(), Claude: s.claude != nil}, nil
}

// loaded returns the PR as last loaded, or an error if the page hasn't loaded it.
func (st *prState) loaded() (*github.PR, string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pr == nil {
		return nil, "", errors.New("this PR isn't loaded; reload the page")
	}
	return st.pr, st.head, nil
}

func (s *Server) handleHead(w http.ResponseWriter, r *http.Request, st *prState) {
	head, err := s.gh.HeadOid(r.Context(), st.owner, st.name, st.number)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]string{"head": head})
}

// handleLines returns lines of a file, for "show more lines".
// side=new reads the head version, side=old the base version.
func (s *Server) handleLines(w http.ResponseWriter, r *http.Request, st *prState) {
	st.mu.Lock()
	base, head := st.base, st.head
	st.mu.Unlock()
	rev := head
	if r.URL.Query().Get("side") == "old" {
		rev = base
	}
	if rev == "" {
		httpError(w, http.StatusConflict, errors.New("this PR isn't loaded; reload the page"))
		return
	}
	lines, err := st.repo.FileLines(r.Context(), rev, r.URL.Query().Get("path"))
	if err != nil {
		httpError(w, http.StatusNotFound, err)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	to, _ := strconv.Atoi(r.URL.Query().Get("to"))
	if from < 1 {
		from = 1
	}
	if to < 1 || to > len(lines) {
		to = len(lines)
	}
	if from > to+1 {
		from = to + 1
	}
	writeJSON(w, map[string]any{"from": from, "lines": lines[from-1 : to], "total": len(lines)})
}

// threadsPayload is the part of a PR that review actions change.
type threadsPayload struct {
	Threads  []github.Thread  `json:"threads"`
	Pending  *github.Review   `json:"pending"`
	Comments []github.Comment `json:"comments"`
	Files    []github.File    `json:"files"`
}

// refresh re-reads the PR's threads and pending review from GitHub after a
// change.
func (s *Server) refresh(ctx context.Context, st *prState) (*threadsPayload, error) {
	pr, err := s.gh.PullRequest(ctx, st.owner, st.name, st.number)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	if st.pr != nil {
		st.pr.Threads, st.pr.Pending, st.pr.Comments, st.pr.Files = pr.Threads, pr.Pending, pr.Comments, pr.Files
	}
	st.mu.Unlock()
	return &threadsPayload{Threads: pr.Threads, Pending: pr.Pending, Comments: pr.Comments, Files: pr.Files}, nil
}

func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request, st *prState) {
	p, err := s.refresh(r.Context(), st)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, p)
}
