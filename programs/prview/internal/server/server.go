// Package server serves the review page and its API.
package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/claude"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/github"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/gitrepo"
)

//go:embed all:dist
var dist embed.FS

// GitHub is what the server needs from GitHub; *github.Client provides it,
// and tests use a fake.
type GitHub interface {
	PullRequest(ctx context.Context, owner, name string, number int) (*github.PR, error)
	HeadOid(ctx context.Context, owner, name string, number int) (string, error)
	SetViewed(ctx context.Context, prID, path string, viewed bool) error
	StartReview(ctx context.Context, prID, commitOid string) (string, error)
	AddThread(ctx context.Context, reviewID string, t github.NewThread) error
	Reply(ctx context.Context, reviewID, threadID, body string) error
	EditComment(ctx context.Context, commentID, body string) error
	DeleteComment(ctx context.Context, commentID string) error
	SetResolved(ctx context.Context, threadID string, resolved bool) error
	SubmitReview(ctx context.Context, reviewID, event, body string) error
	Review(ctx context.Context, prID, commitOid, event, body string) error
	DiscardReview(ctx context.Context, reviewID string) error
}

// Server holds the open PRs and serves them.
type Server struct {
	gh     GitHub
	claude *claude.Runner // nil when claude isn't installed
	cache  string
	key    string

	stop func() // shuts the server down; set by the caller that runs it

	mu    sync.Mutex
	prs   map[string]*prState
	repos map[string]*gitrepo.Repo
}

// OnStop sets what POST /api/stop does.
func (s *Server) OnStop(f func()) { s.stop = f }

// New makes a server. claudeRunner may be nil.
func New(gh GitHub, claudeRunner *claude.Runner, cacheDir, key string) *Server {
	return &Server{gh: gh, claude: claudeRunner, cache: cacheDir, key: key,
		prs: map[string]*prState{}, repos: map[string]*gitrepo.Repo{}}
}

// CookieName holds the access key in the browser (and in `prview stop`).
const CookieName = "prview_key"

// Handler returns the whole HTTP handler, access checks included.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("prview")) })
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) {
		if s.stop == nil {
			httpError(w, http.StatusNotImplemented, errors.New("this server can't be stopped from here"))
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		s.stop()
	})

	api := func(pattern string, h func(http.ResponseWriter, *http.Request, *prState)) {
		method, rest, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" /api/pr/{owner}/{repo}/{number}"+rest, func(w http.ResponseWriter, r *http.Request) {
			st, err := s.state(r)
			if err != nil {
				httpError(w, http.StatusBadRequest, err)
				return
			}
			h(w, r, st)
		})
	}
	api("GET /load", s.handleLoad)
	api("GET /head", s.handleHead)
	api("GET /lines", s.handleLines)
	api("GET /refs", s.handleRefs)
	api("GET /threads", s.handleThreads)
	api("GET /order", s.handleOrder)
	api("POST /order/redo", s.handleOrderRedo)
	api("POST /viewed", s.handleViewed)
	api("POST /comment", s.handleComment)
	api("POST /reply", s.handleReply)
	api("POST /edit", s.handleEdit)
	api("POST /delete", s.handleDelete)
	api("POST /resolve", s.handleResolve)
	api("POST /submit", s.handleSubmit)
	api("POST /discard", s.handleDiscard)
	api("GET /chat", s.handleChat)
	api("POST /ask", s.handleAsk)
	api("POST /draft", s.handleDraft)

	mux.Handle("/", s.static())
	return s.guard(mux)
}

// guard lets a request through only with the key (in the link once, then a
// cookie), and only lets requests that change something through when they
// come from this server's own page.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if k := r.URL.Query().Get("key"); k != "" && s.keyOK(k) {
			http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.key, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteStrictMode, MaxAge: 365 * 24 * 3600})
			q := r.URL.Query()
			q.Del("key")
			u := *r.URL
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(CookieName)
		if err != nil || !s.keyOK(c.Value) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>prview</title><body style="font-family:sans-serif;padding:2em">`+
				`<h1>prview</h1><p>Open the full link that <code>prview</code> printed (it has <code>?key=…</code> at the end) once in this browser.</p>`)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			o := r.Header.Get("Origin")
			ou, err := url.Parse(o)
			if o == "" || err != nil || ou.Host != r.Host {
				httpError(w, http.StatusForbidden, errors.New("request not from prview's own page"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) keyOK(k string) bool {
	return subtle.ConstantTimeCompare([]byte(k), []byte(s.key)) == 1
}

// static serves the built web page; unknown paths get index.html so the
// page can route /owner/repo/pull/n itself.
func (s *Server) static() http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if f, err := sub.Open(p); err == nil {
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "the web page isn't built into this binary; run `make` in the prview repo", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

func (s *Server) state(r *http.Request) (*prState, error) {
	owner, repo := r.PathValue("owner"), r.PathValue("repo")
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || owner == "" || repo == "" || strings.Contains(owner+repo, "..") {
		return nil, errors.New("bad PR address")
	}
	key := fmt.Sprintf("%s/%s/%d", owner, repo, n)
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.prs[key]
	if st == nil {
		rk := owner + "/" + repo
		rp := s.repos[rk]
		if rp == nil {
			rp = gitrepo.Open(s.cache, owner, repo)
			s.repos[rk] = rp
		}
		st = &prState{owner: owner, name: repo, number: n, repo: rp}
		s.prs[key] = st
	}
	return st, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writing response: %v", err)
	}
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20)).Decode(v)
}

// sse starts a server-sent event stream and returns a send function.
func sse(w http.ResponseWriter) func(event string, v any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	var mu sync.Mutex
	return func(event string, v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if fl != nil {
			fl.Flush()
		}
	}
}
