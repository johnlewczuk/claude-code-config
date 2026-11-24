// Package gopls asks the Go language server where a name is defined and
// used, over the language server protocol (JSON-RPC on its stdin and stdout).
package gopls

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Loc is a place in a file. Path is relative to the session's folder, Line
// is 1-based and Col is 1-based in UTF-16 units, as the protocol counts.
type Loc struct {
	Path      string
	Line, Col int
}

// Session is one running gopls, with one folder loaded.
type Session struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	roots []string // the folder, as given and with links followed

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan reply
	dead    error // why the reader stopped; set once

	closeOnce sync.Once
}

// ReplyError is gopls answering a request with an error, such as "no
// identifier found": the session itself is fine.
type ReplyError struct{ Message string }

func (e *ReplyError) Error() string { return "gopls: " + e.Message }

// reply is a response from gopls: a result or an error.
type reply struct {
	result json.RawMessage
	err    error
}

// message is any JSON-RPC message: a request, a response or a notification.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Start runs the gopls program bin in dir, with env added to the
// environment, and finishes the opening exchange. ctx limits only that
// exchange; the session lives until Close.
func Start(ctx context.Context, bin, dir string, env []string) (*Session, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "serve")
	cmd.Dir = abs
	cmd.Env = append(os.Environ(), env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Session{cmd: cmd, in: in, roots: []string{abs}, pending: map[int64]chan reply{}}
	if real, err := filepath.EvalSymlinks(abs); err == nil && real != abs {
		s.roots = append(s.roots, real)
	}
	go s.read(bufio.NewReader(out))

	uri := fileURI(abs)
	_, err = s.call(ctx, "initialize", map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          uri,
		"workspaceFolders": []map[string]string{{"uri": uri, "name": filepath.Base(abs)}},
		"capabilities":     map[string]any{},
	})
	if err == nil {
		err = s.send(message{Method: "initialized", Params: json.RawMessage(`{}`)})
	}
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("starting gopls: %w", err)
	}
	return s, nil
}

// Definition returns where the name at path:line:col is defined.
func (s *Session) Definition(ctx context.Context, path string, line, col int) ([]Loc, error) {
	return s.locations(ctx, "textDocument/definition", path, line, col, nil)
}

// References returns where the name at path:line:col is used, not counting
// its definition.
func (s *Session) References(ctx context.Context, path string, line, col int) ([]Loc, error) {
	return s.locations(ctx, "textDocument/references", path, line, col, map[string]any{"includeDeclaration": false})
}

func (s *Session) locations(ctx context.Context, method, path string, line, col int, lspContext map[string]any) ([]Loc, error) {
	params := map[string]any{
		"textDocument": map[string]string{"uri": fileURI(filepath.Join(s.roots[0], path))},
		"position":     map[string]int{"line": line - 1, "character": col - 1},
	}
	if lspContext != nil {
		params["context"] = lspContext
	}
	raw, err := s.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	// The answer is a list of locations, or of location links, or null.
	var found []struct {
		URI         string   `json:"uri"`
		Range       lspRange `json:"range"`
		TargetURI   string   `json:"targetUri"`
		TargetRange lspRange `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &found); err != nil {
		return nil, fmt.Errorf("reading %s answer: %w", method, err)
	}
	var locs []Loc
	for _, f := range found {
		uri, r := f.URI, f.Range
		if f.TargetURI != "" {
			uri, r = f.TargetURI, f.TargetRange
		}
		if p, ok := s.relPath(uri); ok {
			locs = append(locs, Loc{Path: p, Line: r.Start.Line + 1, Col: r.Start.Character + 1})
		}
	}
	return locs, nil
}

type lspRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
}

// relPath turns a file URI into a path relative to the session's folder. It
// reports false for files outside it, such as the standard library.
func (s *Session) relPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	for _, root := range s.roots {
		rel, err := filepath.Rel(root, filepath.FromSlash(u.Path))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}

func fileURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// Close stops gopls: it asks politely, then kills it after two seconds.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.call(ctx, "shutdown", nil)
		s.send(message{Method: "exit"})
		s.in.Close()
		done := make(chan struct{})
		go func() { s.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			s.cmd.Process.Kill()
			<-done
		}
	})
}

// call sends a request and waits for its answer.
func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan reply, 1)
	s.mu.Lock()
	if s.dead != nil {
		s.mu.Unlock()
		return nil, s.dead
	}
	s.pending[id] = ch
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}

	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			forget()
			return nil, err
		}
		raw = b
	}
	if err := s.send(message{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		forget()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		forget()
		s.send(message{Method: "$/cancelRequest", Params: json.RawMessage(fmt.Sprintf(`{"id":%d}`, id))})
		return nil, ctx.Err()
	}
}

// send writes one message with its Content-Length header.
func (s *Session) send(m message) error {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		message
	}{"2.0", m})
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = fmt.Fprintf(s.in, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// read handles everything gopls sends until its output ends.
func (s *Session) read(r *bufio.Reader) {
	err := s.readLoop(r)
	if err == io.EOF {
		err = errors.New("gopls stopped")
	}
	s.mu.Lock()
	s.dead = err
	for id, ch := range s.pending {
		ch <- reply{err: err}
		delete(s.pending, id)
	}
	s.mu.Unlock()
}

func (s *Session) readLoop(r *bufio.Reader) error {
	for {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return err
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Content-Length") {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n < 0 {
			return errors.New("gopls sent a message without a length")
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return err
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			s.answerRequest(m)
		case m.Method == "":
			s.deliver(m)
		}
		// Notifications (log lines, progress, diagnostics) are ignored.
	}
}

// deliver passes a response on to the call that is waiting for it.
func (s *Session) deliver(m message) {
	id, err := strconv.ParseInt(string(m.ID), 10, 64)
	if err != nil {
		return
	}
	s.mu.Lock()
	ch := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ch == nil {
		return
	}
	if m.Error != nil {
		ch <- reply{err: &ReplyError{Message: m.Error.Message}}
		return
	}
	ch <- reply{result: m.Result}
}

// answerRequest replies to something gopls asks of its client. The answers
// are the plainest ones that let it carry on: no settings, nothing to show.
func (s *Session) answerRequest(m message) {
	result := json.RawMessage("null")
	if m.Method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		json.Unmarshal(m.Params, &p)
		result = json.RawMessage("[" + strings.TrimSuffix(strings.Repeat("null,", len(p.Items)), ",") + "]")
	}
	s.send(message{ID: m.ID, Result: result})
}
