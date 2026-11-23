package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/code"
)

// maxPlaces is how many places /refs sends; the rest are only counted.
const maxPlaces = 300

// refsQuery is what /refs is asked: the name at path:line:col on one side
// of the diff, and the type it was used on when the page knows it.
type refsQuery struct {
	Path, Side, Name, Hint string
	Line, Col              int
}

// refsPayload is the answer to /refs.
type refsPayload struct {
	Name   string       `json:"name"`
	Source string       `json:"source"` // "gopls" or "name"
	Places []code.Place `json:"places"`
	More   int          `json:"more"` // places left out past the cap
}

// handleRefs finds where a name is defined and used, streaming progress and
// ending with a "ready" event carrying a refsPayload.
func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request, st *prState) {
	send := sse(w)
	progress := func(msg string) { send("progress", map[string]string{"message": msg}) }
	v := r.URL.Query()
	q := refsQuery{Path: v.Get("path"), Side: v.Get("side"), Name: v.Get("name"), Hint: v.Get("hint")}
	q.Line, _ = strconv.Atoi(v.Get("line"))
	q.Col, _ = strconv.Atoi(v.Get("col"))
	payload, err := s.refs(r.Context(), st, q, progress)
	if err != nil {
		send("failed", map[string]string{"error": err.Error()})
		return
	}
	send("ready", payload)
}

func (s *Server) refs(ctx context.Context, st *prState, q refsQuery, progress func(string)) (*refsPayload, error) {
	if q.Name == "" {
		return nil, errors.New("no name to look up")
	}
	st.mu.Lock()
	rev := st.head
	if q.Side == "old" {
		rev = st.base
	}
	st.mu.Unlock()
	if rev == "" {
		return nil, errors.New("this PR isn't loaded; reload the page")
	}
	return s.refsByName(ctx, st, rev, q, progress)
}

// refsByName searches every file at rev for the name as a whole word.
func (s *Server) refsByName(ctx context.Context, st *prState, rev string, q refsQuery, progress func(string)) (*refsPayload, error) {
	st.mu.Lock()
	ready := st.treeReady[rev]
	changed := map[string]bool{}
	for _, f := range st.files {
		if q.Side == "old" {
			changed[f.OldPath] = true
		} else {
			changed[f.Path] = true
		}
	}
	st.mu.Unlock()
	if !ready {
		progress("Getting the code ready…")
		if err := st.repo.PrefetchTree(ctx, rev); err != nil {
			return nil, err
		}
		st.mu.Lock()
		if st.treeReady == nil {
			st.treeReady = map[string]bool{}
		}
		st.treeReady[rev] = true
		st.mu.Unlock()
	}
	ms, more, err := st.repo.Grep(ctx, rev, q.Name, maxPlaces)
	if err != nil {
		return nil, err
	}
	return &refsPayload{Name: q.Name, Source: "name", Places: code.Rank(q.Name, q.Hint, q.Path, ms, changed), More: more}, nil
}
