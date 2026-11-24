package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/code"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/gopls"
)

// maxPlaces is how many places /refs sends; the rest are only counted.
const maxPlaces = 300

// grepLimit is how many matches of a name are read before ranking, so a
// definition far down the path order still gets ranked above the uses.
const grepLimit = 3000

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
	payload, err := s.refs(r.Context(), r, st, q, progress)
	if err != nil {
		send("failed", map[string]string{"error": err.Error()})
		return
	}
	send("ready", payload)
}

func (s *Server) refs(ctx context.Context, r *http.Request, st *prState, q refsQuery, progress func(string)) (*refsPayload, error) {
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
	if s.gopls != nil && q.Side != "old" && strings.HasSuffix(q.Path, ".go") {
		p, err := s.refsByGopls(ctx, r, st, q, progress)
		if err == nil && len(p.Places) > 0 {
			return p, nil
		}
		if err != nil {
			log.Printf("gopls couldn't answer for %s: %v; searching by name", q.Name, err)
		}
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
	ms, more, err := st.repo.Grep(ctx, rev, q.Name, grepLimit)
	if err != nil {
		return nil, err
	}
	places := code.Rank(q.Name, q.Hint, q.Path, ms, changed)
	if len(places) > maxPlaces {
		more += len(places) - maxPlaces
		places = places[:maxPlaces]
	}
	return &refsPayload{Name: q.Name, Source: "name", Places: places, More: more}, nil
}

// Time gopls gets for an answer: the first one waits for it to read all the
// code, later ones should be quick.
const (
	goplsFirstWait = 90 * time.Second
	goplsLaterWait = 5 * time.Second
)

// startGopls returns what the pool uses to start gopls in a checkout: with
// every Go module of the checkout loaded together when it has several.
func (s *Server) startGopls(bin string) gopls.StartFunc {
	return func(ctx context.Context, dir string) (gopls.Client, error) {
		rel, err := filepath.Rel(filepath.Join(s.cache, "worktrees"), dir)
		if err != nil {
			return nil, err
		}
		workFile := filepath.Join(s.cache, "gowork", rel, "go.work")
		useWork, err := gopls.WriteGoWork(dir, workFile)
		if err != nil {
			return nil, err
		}
		var env []string
		if useWork {
			env = []string{"GOWORK=" + workFile}
		}
		sess, err := gopls.Start(ctx, bin, dir, env)
		if err != nil {
			return nil, err
		}
		return sess, nil
	}
}

// refsByGopls asks gopls for the definition and the uses of the name at
// the clicked place, in the checkout of the loaded head.
func (s *Server) refsByGopls(ctx context.Context, r *http.Request, st *prState, q refsQuery, progress func(string)) (*refsPayload, error) {
	progress("Getting the code ready…")
	dir, head, err := s.checkout(r, st)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	changed := map[string]bool{}
	for _, f := range st.files {
		changed[f.Path] = true
	}
	st.mu.Unlock()
	key := fmt.Sprintf("%s/%s/%d@%s", st.owner, st.name, st.number, head)

	wait := goplsLaterWait
	if !s.gopls.Answered(key) {
		wait = goplsFirstWait
		progress("Loading the Go code (first time can take a minute)…")
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	client, err := s.gopls.Get(ctx, key, dir)
	if err != nil {
		return nil, err
	}
	defs, err := client.Definition(ctx, q.Path, q.Line, q.Col)
	var uses []gopls.Loc
	if err == nil {
		uses, err = client.References(ctx, q.Path, q.Line, q.Col)
	}
	s.gopls.Finished(key, client, err)
	if err != nil {
		return nil, err
	}
	return goplsPayload(dir, q, defs, uses, changed), nil
}

// goplsPayload turns gopls's places into the answer, reading each line's
// text from the checkout in dir.
func goplsPayload(dir string, q refsQuery, defs, uses []gopls.Loc, changed map[string]bool) *refsPayload {
	files := map[string][]string{}
	place := func(l gopls.Loc, kind string) code.Place {
		lines, ok := files[l.Path]
		if !ok {
			lines = readLines(filepath.Join(dir, l.Path))
			files[l.Path] = lines
		}
		text := ""
		if l.Line >= 1 && l.Line <= len(lines) {
			text = lines[l.Line-1]
		}
		return code.Place{Path: l.Path, Line: l.Line, Col: byteCol(text, l.Col), Text: text, Kind: kind, Changed: changed[l.Path]}
	}
	isDef := map[gopls.Loc]bool{}
	var places []code.Place
	for _, d := range defs {
		isDef[d] = true
		places = append(places, place(d, "definition"))
	}
	for _, u := range uses {
		if !isDef[u] {
			places = append(places, place(u, "use"))
		}
	}
	code.SortPlaces(q.Path, places)
	p := &refsPayload{Name: q.Name, Source: "gopls", Places: places}
	if len(places) > maxPlaces {
		p.More = len(places) - maxPlaces
		p.Places = places[:maxPlaces]
	}
	return p
}

func readLines(file string) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// byteCol turns a 1-based column counted in UTF-16 units into one counted
// in bytes.
func byteCol(text string, col int) int {
	units := 0
	for i, r := range text {
		if units >= col-1 {
			return i + 1
		}
		units += len(utf16.Encode([]rune{r}))
	}
	return len(text) + 1
}
