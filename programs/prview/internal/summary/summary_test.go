package summary

import (
	"reflect"
	"strings"
	"testing"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

func TestSkip(t *testing.T) {
	for _, f := range []diff.File{{Path: "a.png", Binary: true}, {Path: "web/package-lock.json"}, {Path: "api/x.pb.go"}, {Path: "vendor/a/b.go"}} {
		if Skip(f) == "" {
			t.Errorf("%s should be skipped", f.Path)
		}
	}
	if r := Skip(diff.File{Path: "main.go"}); r != "" {
		t.Errorf("main.go skipped: %s", r)
	}
}

func replaceFile() *diff.File {
	return &diff.File{Path: "a.go", Hunks: []diff.Hunk{{OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 3, Lines: []diff.Line{
		{Kind: diff.Context, Old: 10, New: 10, Text: "10"},
		{Kind: diff.Removed, Old: 11, Text: "old11"},
		{Kind: diff.Added, New: 11, Text: "new11"},
		{Kind: diff.Context, Old: 12, New: 12, Text: "12"},
	}}}}
}

func TestMarkSectionNew(t *testing.T) {
	text := make([]string, 12)
	text[9], text[10], text[11] = "10", "new11", "12"
	got := MarkSection(replaceFile(), "new", text, 10, 12)
	want := []MarkedLine{{' ', "10"}, {'-', "old11"}, {'+', "new11"}, {' ', "12"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMarkSectionOld(t *testing.T) {
	text := make([]string, 12)
	text[9], text[10], text[11] = "10", "old11", "12"
	got := MarkSection(replaceFile(), "old", text, 10, 12)
	want := []MarkedLine{{' ', "10"}, {'-', "old11"}, {' ', "12"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestFilePromptCaps(t *testing.T) {
	var lines []diff.Line
	for i := 1; i <= 7000; i++ {
		lines = append(lines, diff.Line{Kind: diff.Added, New: i, Text: "123456789"})
	}
	f := diff.File{Path: "big.go", Hunks: []diff.Hunk{{NewStart: 1, NewLines: 7000, Lines: lines}}}
	if len(DiffText(f)) <= 60000 {
		t.Fatal("test diff too small")
	}
	p := FilePrompt("t", "", f, nil)
	if !strings.Contains(p, "…cut here,") || len(p) > 61000 {
		t.Errorf("not cut: %d bytes", len(p))
	}
}

func TestKeysStable(t *testing.T) {
	a := []MarkedLine{{' ', "x"}, {'+', "y"}}
	b := []MarkedLine{{' ', "x"}, {'-', "y"}}
	if SectionKey("p", a) != SectionKey("p", a) || SectionKey("p", a) == SectionKey("p", b) {
		t.Error("section keys")
	}
	f := *replaceFile()
	if FileKey(f) != FileKey(f) {
		t.Error("file key")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, ok := s.Get("files", "k"); ok {
		t.Fatal("found before put")
	}
	if err := s.Put("files", "k", "hello"); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get("files", "k"); !ok || got != "hello" {
		t.Errorf("got %q %v", got, ok)
	}
}
