// Package claude runs the local `claude` command, locked down to reading
// files: no hooks, skills, plugins' commands or MCP servers, so it can't post
// anywhere. The user's global CLAUDE.md is passed in explicitly, since that
// locked-down mode doesn't load it.
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner runs claude.
type Runner struct {
	Bin          string // path to claude
	Model        string // empty: the user's default
	Instructions string // the user's global instructions, already expanded
}

// NewRunner finds claude and loads ~/.claude/CLAUDE.md with its @imports.
func NewRunner() (*Runner, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, errors.New("the `claude` command isn't on PATH")
	}
	home, _ := os.UserHomeDir()
	return &Runner{Bin: bin, Model: os.Getenv("PRVIEW_MODEL"), Instructions: LoadInstructions(filepath.Join(home, ".claude", "CLAUDE.md"))}, nil
}

// LoadInstructions reads a CLAUDE.md, replacing each line that is just
// "@path" with that file's contents (recursively, once per file).
func LoadInstructions(path string) string {
	return expand(path, map[string]bool{})
}

func expand(path string, seen map[string]bool) string {
	if seen[path] {
		return ""
	}
	seen[path] = true
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	home, _ := os.UserHomeDir()
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "@") && !strings.ContainsAny(t, " \t") && len(t) > 1 {
			p := t[1:]
			if strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[2:])
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(path), p)
			}
			out = append(out, expand(p, seen))
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// readTools are the only tools Claude gets.
const readTools = "Read,Grep,Glob"

func (r *Runner) args(system string, tools string) []string {
	a := []string{"-p",
		"--setting-sources", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands",
		"--settings", `{"disableAllHooks":true}`,
		"--tools", tools,
	}
	if tools != "" {
		a = append(a, "--allowedTools", tools)
	}
	if r.Model != "" {
		a = append(a, "--model", r.Model)
	}
	sys := system
	if strings.TrimSpace(r.Instructions) != "" {
		sys += "\n\n# The user's own instructions (follow them, especially about wording)\n\n" + r.Instructions
	}
	return append(a, "--append-system-prompt", sys)
}

// JSON runs one question with no tools and returns the structured answer,
// shaped by schema, decoded into out.
func (r *Runner) JSON(ctx context.Context, dir, system, prompt, schema string, out any) error {
	args := append(r.args(system, ""), "--output-format", "json", "--json-schema", schema, "--no-session-persistence")
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var res struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("claude gave unreadable output: %w", err)
	}
	if res.IsError || len(res.StructuredOutput) == 0 {
		return fmt.Errorf("claude: %s", res.Result)
	}
	return json.Unmarshal(res.StructuredOutput, out)
}

// Event is one piece of a streamed answer.
type Event struct {
	Kind string `json:"kind"` // "text" (a piece of the answer) or "tool" (Claude is reading something)
	Text string `json:"text"`
}

// Ask runs a question in dir with the read-only tools and streams the answer.
// With sessionID set and resume true it continues that conversation; with
// resume false it starts a conversation with that id.
// It returns the full answer text.
func (r *Runner) Ask(ctx context.Context, dir, system, sessionID string, resume bool, prompt string, on func(Event)) (string, error) {
	args := append(r.args(system, readTools), "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	if sessionID != "" {
		if resume {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", sessionID)
		}
	} else {
		args = append(args, "--no-session-persistence")
	}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var final string
	var failed bool
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		var m streamMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "stream_event":
			if m.Event.Type == "content_block_delta" && m.Event.Delta.Type == "text_delta" {
				on(Event{Kind: "text", Text: m.Event.Delta.Text})
			}
		case "assistant":
			for _, c := range m.Message.Content {
				if c.Type == "tool_use" {
					on(Event{Kind: "tool", Text: strings.ReplaceAll(describeTool(c.Name, c.Input), dir+"/", "")})
				}
			}
		case "result":
			final, failed = m.Result, m.IsError
		}
	}
	err = cmd.Wait()
	if failed {
		return final, fmt.Errorf("claude: %s", final)
	}
	if err != nil {
		return final, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return final, nil
}

type streamMsg struct {
	Type    string `json:"type"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Event   struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Message struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

func describeTool(name string, input json.RawMessage) string {
	var in struct {
		FilePath string `json:"file_path"`
		Pattern  string `json:"pattern"`
		Path     string `json:"path"`
	}
	json.Unmarshal(input, &in)
	switch name {
	case "Read":
		return "Reading " + in.FilePath
	case "Grep":
		return "Searching for " + in.Pattern
	case "Glob":
		return "Listing " + in.Pattern
	}
	return name
}
