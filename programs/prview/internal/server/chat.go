package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/claude"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/github"
)

// Selection is the code a question is about.
type Selection struct {
	Path  string `json:"path"`
	Side  string `json:"side"` // "new" or "old"
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// ChatMessage is one turn of the chat, kept for showing it again.
type ChatMessage struct {
	Role      string     `json:"role"` // "user" or "claude"
	Text      string     `json:"text"`
	Selection *Selection `json:"selection,omitempty"`
	Error     bool       `json:"error,omitempty"`
	At        string     `json:"at"`
}

type chatLog struct {
	SessionID string        `json:"sessionId"`
	Messages  []ChatMessage `json:"messages"`
}

func (s *Server) chatFile(st *prState) string {
	return filepath.Join(s.cache, "chats", st.owner, st.name, fmt.Sprintf("%d.json", st.number))
}

func (s *Server) readChat(st *prState) chatLog {
	var c chatLog
	if b, err := os.ReadFile(s.chatFile(st)); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func (s *Server) writeChat(st *prState, c chatLog) error {
	p := s.chatFile(st)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", " ")
	return os.WriteFile(p, b, 0o644)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request, st *prState) {
	writeJSON(w, s.readChat(st).Messages)
}

// worktree checks out the loaded head into this PR's folder for Claude and
// writes the PR's description and diff next to it, so Claude can read them.
// It returns the folder and the head it checked out.
func (s *Server) worktree(r *http.Request, st *prState) (dir, head string, err error) {
	pr, head, err := st.loaded()
	if err != nil {
		return "", "", err
	}
	dir = filepath.Join(s.cache, "worktrees", st.owner, st.name, fmt.Sprint(st.number))
	if err := st.repo.Worktree(r.Context(), dir, head); err != nil {
		return "", "", err
	}
	st.mu.Lock()
	files, base := st.files, st.base
	st.mu.Unlock()
	info := filepath.Join(dir, ".prview")
	os.MkdirAll(info, 0o755)
	// Keep these out of `git status` in the checkout.
	os.WriteFile(filepath.Join(info, ".gitignore"), []byte("*\n"), 0o644)
	os.WriteFile(filepath.Join(info, "description.md"), []byte("# "+pr.Title+"\n\n"+pr.Body+"\n"), 0o644)
	os.WriteFile(filepath.Join(info, "pr.diff"), []byte(plainDiff(files)), 0o644)
	os.WriteFile(filepath.Join(info, "commits.txt"), []byte(commitList(pr, base)), 0o644)
	return dir, head, nil
}

func plainDiff(files []diff.File) string {
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "--- %s\n+++ %s\n", f.OldPath, f.Path)
		for _, h := range f.Hunks {
			fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@ %s\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines, h.Header)
			for _, l := range h.Lines {
				mark := " "
				switch l.Kind {
				case diff.Added:
					mark = "+"
				case diff.Removed:
					mark = "-"
				}
				b.WriteString(mark + l.Text + "\n")
			}
		}
	}
	return b.String()
}

func commitList(pr *github.PR, base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Base (merge-base) commit: %s\n\n", base)
	for i, c := range pr.Commits {
		fmt.Fprintf(&b, "%d/%d %s %s\n%s\n\n", i+1, len(pr.Commits), c.AbbreviatedOid, c.Author, strings.TrimSpace(c.Message))
	}
	return b.String()
}

func (s *Server) systemPrompt(st *prState) string {
	pr, head, _ := st.loaded()
	return fmt.Sprintf(`You are helping %s review GitHub pull request %s/%s#%d, %q, by %s.

The current folder is a checkout of the PR's head commit %s. You can read and search files but not change anything or run commands.
- .prview/description.md is the PR description.
- .prview/pr.diff is the whole PR diff.
- .prview/commits.txt lists the PR's commits with their messages, oldest first.

Answer the reviewer's questions about the change. Be direct and short. Point at code as path:line. When you're not sure, say so and say what would settle it.`,
		pr.ViewerLogin, st.owner, st.name, st.number, pr.Title, pr.Author, head)
}

// describeSelection writes the selected lines with their commits, for a prompt.
func (st *prState) describeSelection(sel *Selection) string {
	if sel == nil || sel.Path == "" {
		return ""
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	var b strings.Builder
	commits := map[string]bool{}
	fmt.Fprintf(&b, "Selected lines: %s, %s lines %d-%d of the diff:\n```\n", sel.Path, sideWord(sel.Side), sel.Start, sel.End)
	for _, f := range st.files {
		if f.Path != sel.Path && f.OldPath != sel.Path {
			continue
		}
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				n := l.New
				if sel.Side == "old" {
					n = l.Old
				}
				if n < sel.Start || n > sel.End || n == 0 {
					continue
				}
				mark := " "
				switch l.Kind {
				case diff.Added:
					mark = "+"
				case diff.Removed:
					mark = "-"
				}
				b.WriteString(mark + l.Text + "\n")
				if l.Commit != "" {
					commits[l.Commit] = true
				}
			}
		}
	}
	b.WriteString("```\n")
	for i, c := range st.pr.Commits {
		if commits[c.Oid] {
			fmt.Fprintf(&b, "These lines come from commit %d/%d %s: %s\n", i+1, len(st.pr.Commits), c.AbbreviatedOid, strings.TrimSpace(c.Message))
		}
	}
	return b.String()
}

func sideWord(side string) string {
	if side == "old" {
		return "old-side (removed or unchanged)"
	}
	return "new-side"
}

// handleAsk streams Claude's answer to a question about the PR.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		Question  string     `json:"question"`
		Selection *Selection `json:"selection"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if s.claude == nil {
		httpError(w, http.StatusServiceUnavailable, errors.New("the claude command isn't installed"))
		return
	}
	if !st.chatMu.TryLock() {
		httpError(w, http.StatusConflict, errors.New("Claude is still answering the last question"))
		return
	}
	defer st.chatMu.Unlock()

	send := sse(w)
	send("tool", claude.Event{Kind: "tool", Text: "Getting the code ready"})
	dir, _, err := s.worktree(r, st)
	if err != nil {
		send("failed", map[string]string{"error": err.Error()})
		return
	}
	history := s.readChat(st)
	resume := history.SessionID != ""
	if !resume {
		history.SessionID = newUUID()
	}
	now := time.Now().Format(time.RFC3339)
	history.Messages = append(history.Messages, ChatMessage{Role: "user", Text: req.Question, Selection: req.Selection, At: now})

	prompt := req.Question
	if d := st.describeSelection(req.Selection); d != "" {
		prompt = d + "\n" + req.Question
	}
	answer, err := s.claude.Ask(r.Context(), dir, s.systemPrompt(st), history.SessionID, resume, prompt, func(e claude.Event) {
		send(e.Kind, e)
	})
	msg := ChatMessage{Role: "claude", Text: answer, At: time.Now().Format(time.RFC3339)}
	if err != nil {
		msg.Error = true
		if msg.Text == "" {
			msg.Text = err.Error()
		}
		if !resume {
			// The conversation never started; don't try to continue it next time.
			history.SessionID = ""
		}
	}
	history.Messages = append(history.Messages, msg)
	s.writeChat(st, history)
	if err != nil {
		send("failed", map[string]string{"error": msg.Text})
		return
	}
	send("done", msg)
}

// handleDraft streams a review comment Claude writes from a short
// instruction. It isn't part of the chat and posts nothing.
func (s *Server) handleDraft(w http.ResponseWriter, r *http.Request, st *prState) {
	var req struct {
		Instruction string     `json:"instruction"`
		Selection   *Selection `json:"selection"`
		Thread      string     `json:"thread"` // the thread so far, as text, when replying
		Current     string     `json:"current"`
	}
	if err := readJSON(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}
	if s.claude == nil {
		httpError(w, http.StatusServiceUnavailable, errors.New("the claude command isn't installed"))
		return
	}
	send := sse(w)
	dir, _, err := s.worktree(r, st)
	if err != nil {
		send("failed", map[string]string{"error": err.Error()})
		return
	}
	var p strings.Builder
	p.WriteString("Write one GitHub review comment for these lines. Output only the comment text in GitHub markdown: no preamble, no quotes around it, no sign-off.\n\n")
	p.WriteString(st.describeSelection(req.Selection))
	if strings.TrimSpace(req.Thread) != "" {
		p.WriteString("\nThe thread so far:\n" + req.Thread + "\n")
	}
	if strings.TrimSpace(req.Current) != "" {
		p.WriteString("\nThe reviewer's draft so far (improve it, keep its point):\n" + req.Current + "\n")
	}
	p.WriteString("\nWhat the reviewer wants the comment to say: " + req.Instruction + "\n")
	text, err := s.claude.Ask(r.Context(), dir, s.systemPrompt(st), "", false, p.String(), func(e claude.Event) {
		send(e.Kind, e)
	})
	if err != nil {
		send("failed", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]string{"text": strings.TrimSpace(text)})
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
