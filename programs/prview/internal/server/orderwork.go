package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/order"
)

type orderPayload struct {
	Status string       `json:"status"` // "ready", "running", "failed" or "none"
	Order  *order.Order `json:"order,omitempty"`
	Error  string       `json:"error,omitempty"`
}

func (st *prState) orderPayload() orderPayload {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case st.ord != nil:
		return orderPayload{Status: "ready", Order: st.ord}
	case st.ordRunning:
		return orderPayload{Status: "running"}
	case st.ordErr != "":
		return orderPayload{Status: "failed", Error: st.ordErr}
	}
	return orderPayload{Status: "none"}
}

func (s *Server) orderFile(st *prState, head string) string {
	return filepath.Join(s.cache, "orders", st.owner, st.name, fmt.Sprintf("%d-%s.json", st.number, head))
}

// startOrder makes sure a reading order exists or is being written for the
// loaded head: from the description, from the saved copy, or by Claude in
// the background. redo throws away a saved Claude order and asks again.
func (s *Server) startOrder(st *prState, redo bool) {
	st.mu.Lock()
	pr, head, files := st.pr, st.head, st.files
	if pr == nil || (!redo && (st.ord != nil || st.ordRunning)) {
		st.mu.Unlock()
		return
	}
	st.ordGen++
	gen := st.ordGen
	st.ord, st.ordErr = nil, ""
	st.mu.Unlock()

	set := func(o *order.Order, errMsg string) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.ordGen == gen {
			st.ord, st.ordErr, st.ordRunning = o, errMsg, false
		}
	}

	if !redo {
		if o := order.FromDescription(pr.Body); o != nil {
			o.Head = head
			for i := range o.Steps {
				step := &o.Steps[i]
				if lines, err := st.repo.FileLines(context.Background(), head, step.File); err == nil {
					if n := order.Locate(lines, step.Function); n > 0 {
						step.StartLine, step.EndLine = n, n
					}
				}
			}
			set(o, "")
			return
		}
		if b, err := os.ReadFile(s.orderFile(st, head)); err == nil {
			var o order.Order
			if json.Unmarshal(b, &o) == nil {
				set(&o, "")
				return
			}
		}
	}
	if s.claude == nil {
		set(nil, "the claude command isn't installed, so there's no reading order")
		return
	}
	st.mu.Lock()
	st.ordRunning = true
	st.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var o order.Order
		prompt := order.Prompt(pr.Title, pr.Body, order.Numbered(files, 250_000))
		err := s.claude.JSON(ctx, os.TempDir(), order.System, prompt, order.Schema, &o)
		if err != nil {
			log.Printf("reading order for %s/%s#%d: %v", st.owner, st.name, st.number, err)
			set(nil, err.Error())
			return
		}
		o.Source, o.Head = "claude", head
		o.Clean(files)
		if b, err := json.MarshalIndent(&o, "", " "); err == nil {
			p := s.orderFile(st, head)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, b, 0o644)
		}
		set(&o, "")
	}()
}

func (s *Server) handleOrder(w http.ResponseWriter, r *http.Request, st *prState) {
	writeJSON(w, st.orderPayload())
}

func (s *Server) handleOrderRedo(w http.ResponseWriter, r *http.Request, st *prState) {
	s.startOrder(st, true)
	writeJSON(w, st.orderPayload())
}
