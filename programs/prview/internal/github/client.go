// Package github talks to GitHub's GraphQL API with the token from `gh auth token`.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Client calls GitHub's GraphQL API.
type Client struct {
	http *http.Client

	mu    sync.Mutex
	token string
}

// New returns a client. The token is read from `gh auth token` on first use.
func New() *Client {
	return &Client{http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, nil
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("reading the GitHub token with `gh auth token` (are you logged in with gh?): %w", err)
	}
	c.token = strings.TrimSpace(string(out))
	return c.token, nil
}

// Error is an error GitHub returned for a query.
type Error struct{ Messages []string }

func (e *Error) Error() string { return "GitHub: " + strings.Join(e.Messages, "; ") }

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.github.com/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("GitHub answered %s with unreadable data: %w", resp.Status, err)
	}
	if len(res.Errors) > 0 {
		e := &Error{}
		for _, m := range res.Errors {
			e.Messages = append(e.Messages, m.Message)
		}
		return e
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	if len(res.Data) == 0 || string(res.Data) == "null" {
		return errors.New("GitHub returned no data")
	}
	return json.Unmarshal(res.Data, out)
}
