package github

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestLivePullRequest reads a real PR. Read-only. Run with
// PRVIEW_LIVE_PR=owner/name/number go test ./internal/github -run Live -v
func TestLivePullRequest(t *testing.T) {
	spec := os.Getenv("PRVIEW_LIVE_PR")
	if spec == "" {
		t.Skip("set PRVIEW_LIVE_PR=owner/name/number")
	}
	p := strings.Split(spec, "/")
	n, _ := strconv.Atoi(p[2])
	pr, err := New().PullRequest(context.Background(), p[0], p[1], n)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%q by %s: %d commits, %d files, %d threads, %d comments, pending=%v, head=%s",
		pr.Title, pr.Author, len(pr.Commits), len(pr.Files), len(pr.Threads), len(pr.Comments), pr.Pending, pr.HeadRefOid)
	if len(pr.Files) > 0 {
		t.Logf("first file %+v", pr.Files[0])
	}
}
