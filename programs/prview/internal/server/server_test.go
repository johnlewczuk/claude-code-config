package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/github"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/gitrepo"
)

// fakeGitHub records review actions instead of sending them anywhere.
type fakeGitHub struct {
	head  string
	calls []string
}

func (f *fakeGitHub) PullRequest(ctx context.Context, owner, name string, number int) (*github.PR, error) {
	return &github.PR{ID: "PR1", HeadRefOid: f.head}, nil
}
func (f *fakeGitHub) HeadOid(ctx context.Context, owner, name string, number int) (string, error) {
	return f.head, nil
}
func (f *fakeGitHub) SetViewed(ctx context.Context, prID, path string, viewed bool) error {
	f.calls = append(f.calls, "viewed "+path)
	return nil
}
func (f *fakeGitHub) StartReview(ctx context.Context, prID, commitOid string) (string, error) {
	f.calls = append(f.calls, "start "+commitOid)
	return "REV1", nil
}
func (f *fakeGitHub) AddThread(ctx context.Context, reviewID string, t github.NewThread) error {
	f.calls = append(f.calls, "thread "+reviewID+" "+t.Path+" "+t.Side)
	return nil
}
func (f *fakeGitHub) Reply(ctx context.Context, reviewID, threadID, body string) error {
	f.calls = append(f.calls, "reply "+reviewID+" "+threadID)
	return nil
}
func (f *fakeGitHub) EditComment(ctx context.Context, commentID, body string) error { return nil }
func (f *fakeGitHub) DeleteComment(ctx context.Context, commentID string) error     { return nil }
func (f *fakeGitHub) SetResolved(ctx context.Context, threadID string, resolved bool) error {
	return nil
}
func (f *fakeGitHub) SubmitReview(ctx context.Context, reviewID, event, body string) error {
	f.calls = append(f.calls, "submit "+reviewID+" "+event)
	return nil
}
func (f *fakeGitHub) Review(ctx context.Context, prID, commitOid, event, body string) error {
	f.calls = append(f.calls, "review "+commitOid+" "+event)
	return nil
}
func (f *fakeGitHub) DiscardReview(ctx context.Context, reviewID string) error {
	f.calls = append(f.calls, "discard "+reviewID)
	return nil
}

const testKey = "0123456789abcdef0123456789abcdef"

// newTestServer returns a server with PR o/r#1 already loaded at head "h1".
func newTestServer(t *testing.T) (*Server, *fakeGitHub, http.Handler) {
	gh := &fakeGitHub{head: "h1"}
	s := New(gh, nil, t.TempDir(), testKey)
	s.prs["o/r/1"] = &prState{owner: "o", name: "r", number: 1, repo: &gitrepo.Repo{},
		pr: &github.PR{ID: "PR1", HeadRefOid: "h1"}, head: "h1"}
	return s, gh, s.Handler()
}

func do(h http.Handler, method, path, body string, auth bool, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://prview.test"+path, strings.NewReader(body))
	if auth {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: testKey})
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const own = "http://prview.test"

func TestGuard(t *testing.T) {
	_, gh, h := newTestServer(t)
	if w := do(h, "GET", "/api/pr/o/r/1/order", "", false, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", w.Code)
	}
	w := do(h, "GET", "/o/r/pull/1?key="+testKey, "", false, "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/o/r/pull/1" {
		t.Errorf("key link: got %d to %q", w.Code, w.Header().Get("Location"))
	}
	if c := w.Result().Cookies(); len(c) != 1 || c[0].Value != testKey || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie: %+v", c)
	}
	if w := do(h, "GET", "/o/r/pull/1?key=wrong", "", false, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: got %d", w.Code)
	}
	viewed := `{"path":"a.go","viewed":true}`
	if w := do(h, "POST", "/api/pr/o/r/1/viewed", viewed, true, ""); w.Code != http.StatusForbidden {
		t.Errorf("no origin: got %d", w.Code)
	}
	if w := do(h, "POST", "/api/pr/o/r/1/viewed", viewed, true, "http://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("other origin: got %d", w.Code)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("refused requests reached GitHub: %v", gh.calls)
	}
	if w := do(h, "POST", "/api/pr/o/r/1/viewed", viewed, true, own); w.Code != http.StatusOK {
		t.Errorf("own page: got %d %s", w.Code, w.Body)
	}
}

func TestCommentStartsReviewOnLoadedHead(t *testing.T) {
	_, gh, h := newTestServer(t)
	w := do(h, "POST", "/api/pr/o/r/1/comment", `{"path":"a.go","body":"hi","line":3,"side":"RIGHT","head":"h1"}`, true, own)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if got := strings.Join(gh.calls, "; "); got != "start h1; thread REV1 a.go RIGHT" {
		t.Fatalf("calls: %s", got)
	}
}

func TestRefusedWhenThePRMoved(t *testing.T) {
	_, gh, h := newTestServer(t)
	gh.head = "h2" // the author pushed after the page loaded

	w := do(h, "POST", "/api/pr/o/r/1/comment", `{"path":"a.go","body":"hi","line":3,"side":"RIGHT","head":"h1"}`, true, own)
	if w.Code != http.StatusConflict {
		t.Errorf("comment: got %d", w.Code)
	}
	for _, ev := range []string{"APPROVE", "REQUEST_CHANGES"} {
		w = do(h, "POST", "/api/pr/o/r/1/submit", `{"event":"`+ev+`","head":"h1"}`, true, own)
		if w.Code != http.StatusConflict {
			t.Errorf("%s: got %d", ev, w.Code)
		}
	}
	if len(gh.calls) != 0 {
		t.Fatalf("refused actions reached GitHub: %v", gh.calls)
	}
	// A plain comment review is still allowed.
	w = do(h, "POST", "/api/pr/o/r/1/submit", `{"event":"COMMENT","body":"looks fine","head":"h1"}`, true, own)
	if w.Code != http.StatusOK || strings.Join(gh.calls, "; ") != "review h1 COMMENT" {
		t.Fatalf("comment review: %d %v", w.Code, gh.calls)
	}
}

func TestSubmitUsesPendingReview(t *testing.T) {
	s, gh, h := newTestServer(t)
	s.prs["o/r/1"].pr.Pending = &github.Review{ID: "REV9"}
	w := do(h, "POST", "/api/pr/o/r/1/submit", `{"event":"APPROVE","head":"h1"}`, true, own)
	if w.Code != http.StatusOK || strings.Join(gh.calls, "; ") != "submit REV9 APPROVE" {
		t.Fatalf("got %d %v", w.Code, gh.calls)
	}
}

func TestStop(t *testing.T) {
	s, _, h := newTestServer(t)
	stopped := 0
	s.OnStop(func() { stopped++ })
	if w := do(h, "POST", "/api/stop", "", false, own); w.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", w.Code)
	}
	if w := do(h, "POST", "/api/stop", "", true, "http://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("other origin: got %d", w.Code)
	}
	if stopped != 0 {
		t.Fatal("refused requests stopped the server")
	}
	if w := do(h, "POST", "/api/stop", "", true, own); w.Code != http.StatusOK || stopped != 1 {
		t.Fatalf("own page: got %d, stopped %d", w.Code, stopped)
	}
}
