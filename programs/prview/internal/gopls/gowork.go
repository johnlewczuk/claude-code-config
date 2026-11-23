package gopls

import (
	"go/version"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// WriteGoWork writes a go.work file to workFile that loads every Go module
// under checkout together, so a name can be followed from one module into
// another. It returns whether gopls must be pointed at it (with GOWORK).
// It writes nothing and returns false when the checkout has its own go.work
// or has no go.mod.
func WriteGoWork(checkout, workFile string) (useEnv bool, err error) {
	root, err := filepath.Abs(checkout)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err == nil {
		return false, nil
	}
	var mods []string
	goVer := "" // the newest go line of the modules: the workspace needs at least that
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (name == "vendor" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			mods = append(mods, filepath.Dir(p))
			if v := goLine(p); version.Compare("go"+v, "go"+goVer) > 0 {
				goVer = v
			}
		}
		return nil
	})
	if err != nil || len(mods) == 0 {
		return false, err
	}
	sort.Strings(mods)
	var b strings.Builder
	if goVer != "" {
		b.WriteString("go " + goVer + "\n\n")
	}
	b.WriteString("use (\n")
	for _, m := range mods {
		if strings.ContainsAny(m, " \t\"'`") {
			m = strconv.Quote(m)
		}
		b.WriteString("\t" + m + "\n")
	}
	b.WriteString(")\n")
	if err := os.MkdirAll(filepath.Dir(workFile), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(workFile, []byte(b.String()), 0o644)
}

// goLine returns the Go version a go.mod file asks for, or "" if none.
func goLine(goMod string) string {
	b, err := os.ReadFile(goMod)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "go" && version.IsValid("go"+f[1]) {
			return f[1]
		}
	}
	return ""
}
