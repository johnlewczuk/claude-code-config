package gopls

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client is what the pool keeps: a Session, or a stand-in for tests.
type Client interface {
	Definition(ctx context.Context, path string, line, col int) ([]Loc, error)
	References(ctx context.Context, path string, line, col int) ([]Loc, error)
	Close()
}

// StartFunc starts a client for the folder dir.
type StartFunc func(ctx context.Context, dir string) (Client, error)

// Pool keeps a few sessions running, one per PR head, and stops the ones
// that sit unused or stop working.
type Pool struct {
	start StartFunc
	idle  time.Duration
	max   int

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
	quit    chan struct{}
}

type entry struct {
	ready    chan struct{} // closed once the start has finished
	client   Client        // nil while starting
	err      error
	last     time.Time
	answered bool // a lookup has had an answer from this session
}

// errStopped is what a start finds when its session was stopped before it
// finished starting: for a new head, the size limit, being idle or Close.
var errStopped = errors.New("gopls was stopped while it started")

// NewPool makes a pool that closes sessions unused for idle, and the least
// recently used one when more than max would be open.
func NewPool(start StartFunc, idle time.Duration, max int) *Pool {
	p := &Pool{start: start, idle: idle, max: max, entries: map[string]*entry{}, quit: make(chan struct{})}
	go p.reap()
	return p
}

// group is the PR part of a key "owner/repo/number@head".
func group(key string) string {
	g, _, _ := strings.Cut(key, "@")
	return g
}

// Get returns the client for key, starting one in dir if there is none. A
// new head for a PR stops the session of its old head. Gets for a key that
// is still starting wait for the same start.
func (p *Pool) Get(ctx context.Context, key, dir string) (Client, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, context.Canceled
	}
	e := p.entries[key]
	var victims []Client
	starter := e == nil
	if starter {
		e = &entry{ready: make(chan struct{})}
		p.entries[key] = e
		for k := range p.entries {
			if k != key && group(k) == group(key) {
				victims = append(victims, p.drop(k))
			}
		}
		victims = append(victims, p.trim(key)...)
	}
	e.last = time.Now()
	p.mu.Unlock()
	closeAll(victims)

	if starter {
		c, err := p.start(ctx, dir)
		var stale Client
		p.mu.Lock()
		kept := p.entries[key] == e
		switch {
		case err != nil && kept:
			delete(p.entries, key)
		case err == nil && !kept:
			stale, c, err = c, nil, errStopped
		}
		e.client, e.err = c, err
		p.mu.Unlock()
		closeAll([]Client{stale})
		close(e.ready)
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return e.client, e.err
}

// Answered reports whether the running session for key has answered a
// lookup, so the next one can expect a quick reply. A session that is
// starting, or was stopped, has not.
func (p *Pool) Answered(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[key]
	return e != nil && e.answered
}

// Finished records how a lookup on c, the client Get gave for key, went. An
// answer marks the session as answering. A gopls error reply (no name at
// that place, say) or a lookup the caller cancelled says nothing about the
// session. Anything else (gopls stopped, or gave no answer in time) means
// the session is broken: it is stopped, so the next Get starts a fresh one.
func (p *Pool) Finished(key string, c Client, err error) {
	var re *ReplyError
	if errors.As(err, &re) || errors.Is(err, context.Canceled) {
		return
	}
	p.mu.Lock()
	e := p.entries[key]
	if e == nil || e.client != c {
		p.mu.Unlock()
		return
	}
	var broken Client
	if err == nil {
		e.answered = true
	} else {
		broken = p.drop(key)
	}
	p.mu.Unlock()
	closeAll([]Client{broken})
}

// drop forgets the entry for key and returns its client, nil when it is
// still starting (its start then closes it). The caller holds mu.
func (p *Pool) drop(key string) Client {
	c := p.entries[key].client
	delete(p.entries, key)
	return c
}

func closeAll(cs []Client) {
	for _, c := range cs {
		if c != nil {
			c.Close()
		}
	}
}

// trim drops the least recently used entries, other than keep, until the
// pool is within its limit. The caller holds mu.
func (p *Pool) trim(keep string) []Client {
	var keys []string
	for k := range p.entries {
		if k != keep {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return p.entries[keys[i]].last.Before(p.entries[keys[j]].last) })
	var out []Client
	for len(p.entries) > p.max && len(keys) > 0 {
		out = append(out, p.drop(keys[0]))
		keys = keys[1:]
	}
	return out
}

// reap closes sessions that have been idle too long.
func (p *Pool) reap() {
	tick := p.idle / 4
	if tick < 5*time.Millisecond {
		tick = 5 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-t.C:
		}
		var victims []Client
		p.mu.Lock()
		for k, e := range p.entries {
			if time.Since(e.last) > p.idle {
				victims = append(victims, p.drop(k))
			}
		}
		p.mu.Unlock()
		closeAll(victims)
	}
}

// Close closes every session and stops the pool.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.quit)
	var all []Client
	for k := range p.entries {
		all = append(all, p.drop(k))
	}
	p.mu.Unlock()
	closeAll(all)
}
