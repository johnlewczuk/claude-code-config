package gopls

import (
	"context"
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
// that sit unused.
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
	ready  chan struct{} // closed once the start has finished
	client Client
	err    error
	last   time.Time
}

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
// new head for a PR closes the client of its old head. Gets for a key that
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
		for k, o := range p.entries {
			if k != key && group(k) == group(key) && o.client != nil {
				victims = append(victims, p.drop(k))
			}
		}
		victims = append(victims, p.trim(key)...)
	}
	e.last = time.Now()
	p.mu.Unlock()
	for _, v := range victims {
		v.Close()
	}

	if starter {
		c, err := p.start(ctx, dir)
		p.mu.Lock()
		e.client, e.err = c, err
		if err != nil {
			delete(p.entries, key)
		}
		p.mu.Unlock()
		close(e.ready)
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return e.client, e.err
}

// drop forgets the entry for key and returns its client. The caller holds mu.
func (p *Pool) drop(key string) Client {
	c := p.entries[key].client
	delete(p.entries, key)
	return c
}

// trim drops the least recently used started entries, other than keep,
// until the pool is within its limit. The caller holds mu.
func (p *Pool) trim(keep string) []Client {
	var keys []string
	for k, e := range p.entries {
		if k != keep && e.client != nil {
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

// reap closes clients that have been idle too long.
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
			if e.client != nil && time.Since(e.last) > p.idle {
				victims = append(victims, p.drop(k))
			}
		}
		p.mu.Unlock()
		for _, v := range victims {
			v.Close()
		}
	}
}

// Close closes every client and stops the pool.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.quit)
	var all []Client
	for k, e := range p.entries {
		if e.client != nil {
			all = append(all, p.drop(k))
		}
	}
	p.mu.Unlock()
	for _, c := range all {
		c.Close()
	}
}
