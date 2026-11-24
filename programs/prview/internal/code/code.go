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

// A definition is read from the start of the line: modifiers, then at most
// one definition word (with what may follow it), then more modifiers, then
// the name being defined.
var (
	modifiers = regexp.MustCompile(`^(?:(?:pub(?:\([^)]*\))?|static|async|export|default|public|private|protected|internal|abstract|final|override|virtual|inline|extern|unsafe|readonly|declare|constexpr|mut)\s+)+`)
	defWord   = regexp.MustCompile(`^(?:(func|def|fn|fun|class|struct|interface|protocol|type|enum|impl|trait|union|namespace|module|let|var|val|const)(?:\s+|$)|function\b\s*\*?\s*|#\s*define\s+)`)
	// Go's method receiver, after func.
	receiver = regexp.MustCompile(`^\([^)]*\)\s*`)
	// Type parameters, as in impl<T> Foo.
	typeParams = regexp.MustCompile(`^<[^>]*>\s*`)
	// The first name, maybe qualified (Type::Name or a.b.Name).
	firstName = regexp.MustCompile(`^(?:\w+(?:::|\.))*(\w+)`)
	// C and C++ style: a type (a return or variable type), then the name
	// declared, maybe qualified, then what can follow a declared name. The
	// type can't hold "(", "=", "." or a lone ":", so it can't reach past the
	// first call or assignment on the line.
	declarator = regexp.MustCompile(`^((?:\w|::|[<>,*&\s\[\]])*?[\w>\]][\s*&]+)?(?:\w+::)*~?(\w+)\s*(\(|=[^=]|;|\{|\[|:[^:])`)
	words      = regexp.MustCompile(`\w+`)
)

// cTypeWords are definition words that can also begin a C or C++ type, as
// in "const std::string& name()" or "struct Foo make()".
var cTypeWords = map[string]bool{"const": true, "struct": true, "enum": true, "union": true}

// statementWords begin statements, not declarations: "return helper()" uses
// helper.
var statementWords = map[string]bool{
	"return": true, "if": true, "elif": true, "else": true, "while": true, "for": true, "in": true, "of": true,
	"do": true, "switch": true, "case": true, "new": true, "delete": true, "throw": true, "raise": true,
	"await": true, "yield": true, "go": true, "defer": true, "not": true, "and": true, "or": true, "is": true,
	"assert": true, "with": true, "goto": true, "sizeof": true, "typeof": true, "instanceof": true,
	"co_return": true, "co_await": true, "co_yield": true, "del": true, "lambda": true, "break": true,
	"continue": true, "import": true, "from": true, "package": true, "using": true, "emit": true, "as": true,
	"print": true, "echo": true,
}

// IsDefinition reports whether line defines name rather than uses it. name
// has to be the thing defined, not just appear on the line: "func (s
// *Server) Run()" defines Run, not Server, and "const user = getUser(id)"
// defines user, not getUser.
func IsDefinition(line, name string) bool {
	defined, _ := DefinedName(line)
	return name != "" && defined == name
}

// commentPrefixes gives the ways a whole-line comment can start, by file
// extension. Files of unknown language use "#".
func commentPrefixes(file string) []string {
	switch strings.ToLower(path.Ext(file)) {
	case ".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".java", ".js", ".jsx", ".ts", ".tsx", ".rs", ".cs", ".swift", ".kt", ".proto", ".css", ".scss", ".php":
		// "* " continues a /* */ block; "*out = x" is code.
		return []string{"//", "/*", "*/", "* ", "*\t"}
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
	// The space added lets a bare "*" line match "* ".
	t := strings.TrimSpace(line) + " "
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
