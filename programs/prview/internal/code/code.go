// Package code tells definitions from uses in source lines and ranks the
// places a name appears, for code navigation.
package code

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Match is one line of a search for a name.
type Match struct {
	Path      string
	Line, Col int // Col is 1-based, in bytes
	Text      string
}

// Place is a match worked out for showing: what it is and how it relates to
// the PR.
type Place struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Text    string `json:"text"`
	Kind    string `json:"kind"` // "definition", "use" or "comment"
	Changed bool   `json:"changed"`
}

// defWords starts a definition in common languages.
var defWords = regexp.MustCompile(`^\s*(func|def|fn|function|class|struct|interface|type|enum|impl|pub|static|async|export|public|private|protected|void|const|let|var)\b`)

func wordRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_])`)
}

// IsDefinition reports whether line defines name rather than uses it.
func IsDefinition(line, name string) bool {
	t := strings.TrimSpace(line)
	if loc := defWords.FindStringIndex(line); loc != nil && wordRe(name).MatchString(line[loc[1]:]) {
		return true
	}
	if strings.Contains(line, "::"+name+"(") {
		return true
	}
	// C and C++: the name and "(" at the start of an unindented line that
	// doesn't end in ";".
	return strings.Contains(line, name+"(") && !strings.HasSuffix(t, ";") &&
		!strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t")
}

// commentPrefixes gives the ways a whole-line comment can start, by file
// extension. Files of unknown language use "#".
func commentPrefixes(file string) []string {
	switch strings.ToLower(path.Ext(file)) {
	case ".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".java", ".js", ".jsx", ".ts", ".tsx", ".rs", ".cs", ".swift", ".kt", ".proto", ".css", ".scss", ".php":
		return []string{"//", "/*", "*"}
	case ".py", ".sh", ".bash", ".zsh", ".yaml", ".yml", ".cmake", ".rb", ".toml", ".mk":
		return []string{"#"}
	case ".sql", ".lua":
		return []string{"--"}
	case ".xml", ".html", ".htm", ".md":
		return []string{"<!--"}
	}
	return []string{"#"}
}

// IsCommentOnly reports whether the whole line is a comment.
func IsCommentOnly(file, line string) bool {
	t := strings.TrimSpace(line)
	for _, p := range commentPrefixes(file) {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

var kindOrder = map[string]int{"definition": 0, "use": 1, "comment": 2}

// SortPlaces orders places within a group: the file the name was clicked in
// first, then files changed in the PR, then files in the same folder, then
// by path and line. It puts definitions before uses before comments.
func SortPlaces(fromPath string, ps []Place) {
	sortPlaces(fromPath, ps, nil)
}

func sortPlaces(fromPath string, ps []Place, first func(Place) bool) {
	dir := path.Dir(fromPath)
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if ka, kb := kindOrder[a.Kind], kindOrder[b.Kind]; ka != kb {
			return ka < kb
		}
		if first != nil {
			if fa, fb := first(a), first(b); fa != fb {
				return fa
			}
		}
		if sa, sb := a.Path == fromPath, b.Path == fromPath; sa != sb {
			return sa
		}
		if a.Changed != b.Changed {
			return a.Changed
		}
		if da, db := path.Dir(a.Path) == dir, path.Dir(b.Path) == dir; da != db {
			return da
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
}

// Rank classifies matches of name and puts the likeliest first. hint is the
// type the name was used on, when known: a definition inside that type
// comes first. fromPath is the file the name was clicked in; changed holds
// the paths the PR changed.
func Rank(name, hint, fromPath string, ms []Match, changed map[string]bool) []Place {
	ps := make([]Place, 0, len(ms))
	for _, m := range ms {
		kind := "use"
		switch {
		case IsCommentOnly(m.Path, m.Text):
			kind = "comment"
		case IsDefinition(m.Text, name):
			kind = "definition"
		}
		ps = append(ps, Place{Path: m.Path, Line: m.Line, Col: m.Col, Text: m.Text, Kind: kind, Changed: changed[m.Path]})
	}
	var first func(Place) bool
	if hint != "" {
		recv := regexp.MustCompile(`\(\s*\w+\s+\*?` + regexp.QuoteMeta(hint) + `\s*\)`)
		first = func(p Place) bool {
			return p.Kind == "definition" && (strings.Contains(p.Text, hint+"::"+name) || recv.MatchString(p.Text))
		}
	}
	sortPlaces(fromPath, ps, first)
	return ps
}
