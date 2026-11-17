package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/github"
)

// errMoved is returned when the PR got new commits since the page loaded.
var errMoved = errors.New("the PR has new commits since you loaded it; reload first (your text is kept)")

// checkHead refuses the action if GitHub's head isn't the head the page shows.
func (s *Server) checkHead(ctx context.Context, st *prState, pageHead string) error {
	_, loadedHead, err := st.loaded()
	if err != nil {
		return err
	}
	cur, err := s.gh.HeadOid(ctx, st.owner, st.name, st.number)
	if err != nil {
		return err
	}
	if cur != loadedHead || (pageHead != "" && pageHead != cur) {
		return errMoved
	}
	return nil
}

// pendingReview returns my pending review, starting one on head if needed.
func (s *Server) pendingReview(ctx context.Context, st *prState) (string, error) {
	pr, head, err := st.loaded()
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	pending := pr.Pending
	st.mu.Unlock()
	if pending != nil {
		return pending.ID, nil
	}
	id, err := s.gh.StartReview(ctx, pr.ID, head)
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	pr.Pending = &github.Review{ID: id, CommitOid: head}
	st.mu.Unlock()
	return id, nil
}

// respond answers a review action with the refreshed threads.
func (s *Server) respond(w http.ResponseWriter, r *http.Request, st *prState, err error) {
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errMoved) {
			code = http.StatusConflict
		}
		httpError(w, code, err)
		return
	}
	p, err := s.refresh(r.Context(), st)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, p)
}

func (s *Server) handleViewed(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		Path   string `json:"path"`
		Viewed bool   `json:"viewed"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	pr, _, err := st.loaded()
	if err == nil {
		err = s.gh.SetViewed(r.Context(), pr.ID, req.Path, req.Viewed)
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleComment(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		github.NewThread
		Head string `json:"head"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		httpError(w, http.StatusBadRequest, errors.New("the comment is empty"))
		return
	}
	err := s.checkHead(r.Context(), st, req.Head)
	var review string
	if err == nil {
		review, err = s.pendingReview(r.Context(), st)
	}
	if err == nil {
		err = s.gh.AddThread(r.Context(), review, req.NewThread)
	}
	s.respond(w, r, st, err)
}

func (s *Server) handleReply(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		ThreadID string `json:"threadId"`
		Body     string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		httpError(w, http.StatusBadRequest, errors.New("the reply is empty"))
		return
	}
	review, err := s.pendingReview(r.Context(), st)
	if err == nil {
		err = s.gh.Reply(r.Context(), review, req.ThreadID, req.Body)
	}
	s.respond(w, r, st, err)
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		CommentID string `json:"commentId"`
		Body      string `json:"body"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	s.respond(w, r, st, s.gh.EditComment(r.Context(), req.CommentID, req.Body))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		CommentID string `json:"commentId"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	s.respond(w, r, st, s.gh.DeleteComment(r.Context(), req.CommentID))
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		ThreadID string `json:"threadId"`
		Resolved bool   `json:"resolved"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	s.respond(w, r, st, s.gh.SetResolved(r.Context(), req.ThreadID, req.Resolved))
}

// handleSubmit submits the review. Approve and Request changes are refused
// when the PR moved since the page loaded, so nobody approves unseen code.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		Event string `json:"event"`
		Body  string `json:"body"`
		Head  string `json:"head"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	switch req.Event {
	case github.EventComment, github.EventApprove, github.EventRequestChanges:
	default:
		httpError(w, http.StatusBadRequest, errors.New("unknown review type"))
		return
	}
	pr, head, err := st.loaded()
	if err != nil {
		httpError(w, http.StatusConflict, err)
		return
	}
	if req.Event != github.EventComment {
		if err := s.checkHead(r.Context(), st, req.Head); err != nil {
			s.respond(w, r, st, err)
			return
		}
	}
	st.mu.Lock()
	pending := pr.Pending
	st.mu.Unlock()
	switch {
	case pending != nil:
		err = s.gh.SubmitReview(r.Context(), pending.ID, req.Event, req.Body)
	case req.Event == github.EventComment && strings.TrimSpace(req.Body) == "":
		err = errors.New("nothing to submit: no draft comments and no summary")
	default:
		err = s.gh.Review(r.Context(), pr.ID, head, req.Event, req.Body)
	}
	s.respond(w, r, st, err)
}

func (s *Server) handleDiscard(w http.ResponseWriter, r *http.Request, st *prState) {
	pr, _, err := st.loaded()
	if err != nil {
		httpError(w, http.StatusConflict, err)
		return
	}
	st.mu.Lock()
	pending := pr.Pending
	st.mu.Unlock()
	if pending == nil {
		s.respond(w, r, st, nil)
		return
	}
	s.respond(w, r, st, s.gh.DiscardReview(r.Context(), pending.ID))
}
