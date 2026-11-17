package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnlewczuk/claude-code-config/programs/prview/internal/diff"
)

// TestLive runs the whole git path on a real PR. Read-only on GitHub. Run with
// PRVIEW_LIVE_PR=owner/name/number:baseBranch go test ./internal/gitrepo -run Live -v
func TestLive(t *testing.T) {
	spec := os.Getenv("PRVIEW_LIVE_PR")
	if spec == "" {
		t.Skip("set PRVIEW_LIVE_PR=owner/name/number:base")
	}
	spec, baseRef, _ := strings.Cut(spec, ":")
	p := strings.Split(spec, "/")
	n, _ := strconv.Atoi(p[2])
	home, _ := os.UserHomeDir()
	r := Open(filepath.Join(home, ".cache", "prview"), p[0], p[1])
	ctx := context.Background()
	start := time.Now()
	if err := r.Fetch(ctx, n, baseRef, nil); err != nil {
		t.Fatal(err)
	}
	head := "refs/prview/pull/" + p[2]
	base, err := r.MergeBase(ctx, "refs/prview/base/"+baseRef, head)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fetched in %v, base %s", time.Since(start), base[:8])
	start = time.Now()
	paths, err := r.ChangedPaths(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.PrefetchBlobs(ctx, base, head, paths); err != nil {
		t.Fatal(err)
	}
	t.Logf("prefetch in %v", time.Since(start))
	start = time.Now()
	files, err := r.Diff(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Attribute(ctx, base, head, files); err != nil {
		t.Fatal(err)
	}
	t.Logf("diff+attribute in %v: %d files", time.Since(start), len(files))
	counts := map[string]int{}
	missing := 0
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind != diff.Context {
					if l.Commit == "" {
						missing++
					} else {
						counts[l.Commit[:8]]++
					}
				}
			}
		}
	}
	t.Logf("lines per commit %v, changed lines with no commit: %d", counts, missing)
}
