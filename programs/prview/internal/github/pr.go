package github

import (
	"context"
	"fmt"
)

// PR is everything the review page needs from GitHub about one pull request.
type PR struct {
	ID          string    `json:"id"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	BodyHTML    string    `json:"bodyHTML"`
	Body        string    `json:"body"`
	URL         string    `json:"url"`
	State       string    `json:"state"`
	IsDraft     bool      `json:"isDraft"`
	Author      string    `json:"author"`
	BaseRefName string    `json:"baseRefName"`
	HeadRefName string    `json:"headRefName"`
	HeadRefOid  string    `json:"headRefOid"`
	Commits     []Commit  `json:"commits"`
	Files       []File    `json:"files"`
	Threads     []Thread  `json:"threads"`
	Comments    []Comment `json:"comments"` // the general conversation
	Pending     *Review   `json:"pending"`  // my pending review, if any
	ViewerLogin string    `json:"viewerLogin"`
}

// Commit is one commit of the PR.
type Commit struct {
	Oid             string `json:"oid"`
	AbbreviatedOid  string `json:"abbreviatedOid"`
	MessageHeadline string `json:"messageHeadline"`
	Message         string `json:"message"`
	AuthoredDate    string `json:"authoredDate"`
	Author          string `json:"author"`
	URL             string `json:"url"`
}

// File is one changed file, with my Viewed mark.
type File struct {
	Path   string `json:"path"`
	Viewed string `json:"viewed"` // VIEWED, UNVIEWED or DISMISSED (changed since viewed)
}

// Thread is one review thread on a line or range.
type Thread struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	IsResolved    bool      `json:"isResolved"`
	IsOutdated    bool      `json:"isOutdated"`
	Line          int       `json:"line"`
	StartLine     int       `json:"startLine"`
	OriginalLine  int       `json:"originalLine"`
	OriginalStart int       `json:"originalStartLine"`
	Side          string    `json:"diffSide"`
	StartSide     string    `json:"startDiffSide"`
	SubjectType   string    `json:"subjectType"`
	CanReply      bool      `json:"viewerCanReply"`
	CanResolve    bool      `json:"viewerCanResolve"`
	CanUnresolve  bool      `json:"viewerCanUnresolve"`
	Comments      []Comment `json:"comments"`
}

// Comment is one comment in a thread or in the conversation.
type Comment struct {
	ID         string `json:"id"`
	Author     string `json:"author"`
	Body       string `json:"body"`
	BodyHTML   string `json:"bodyHTML"`
	CreatedAt  string `json:"createdAt"`
	State      string `json:"state,omitempty"` // PENDING for my draft comments
	DiffHunk   string `json:"diffHunk,omitempty"`
	CanUpdate  bool   `json:"viewerCanUpdate"`
	CanDelete  bool   `json:"viewerCanDelete"`
	URL        string `json:"url,omitempty"`
}

// Review is a pending review.
type Review struct {
	ID        string `json:"id"`
	CommitOid string `json:"commitOid"`
}

const prQuery = `
query($owner:String!, $name:String!, $number:Int!, $files:String, $threads:String) {
  viewer { login }
  repository(owner:$owner, name:$name) {
    pullRequest(number:$number) {
      id number title body bodyHTML url state isDraft
      author { login }
      baseRefName headRefName headRefOid
      commits(first:250) { nodes { url commit {
        oid abbreviatedOid messageHeadline message authoredDate
        author { name user { login } }
      } } }
      files(first:100, after:$files) {
        pageInfo { hasNextPage endCursor }
        nodes { path viewerViewedState }
      }
      reviewThreads(first:100, after:$threads) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id path isResolved isOutdated line startLine originalLine originalStartLine
          diffSide startDiffSide subjectType viewerCanReply viewerCanResolve viewerCanUnresolve
          comments(first:100) { nodes { ...c state diffHunk } }
        }
      }
      comments(first:100) { nodes { ...ic } }
      reviews(first:20, states:[PENDING]) { nodes { id viewerDidAuthor commit { oid } } }
    }
  }
}
fragment c on PullRequestReviewComment {
  id body bodyHTML createdAt url viewerCanUpdate viewerCanDelete author { login }
}
fragment ic on IssueComment {
  id body bodyHTML createdAt url viewerCanUpdate viewerCanDelete author { login }
}`

type rawComment struct {
	ID              string `json:"id"`
	Body            string `json:"body"`
	BodyHTML        string `json:"bodyHTML"`
	CreatedAt       string `json:"createdAt"`
	URL             string `json:"url"`
	State           string `json:"state"`
	DiffHunk        string `json:"diffHunk"`
	ViewerCanUpdate bool   `json:"viewerCanUpdate"`
	ViewerCanDelete bool   `json:"viewerCanDelete"`
	Author          *struct {
		Login string `json:"login"`
	} `json:"author"`
}

func (r rawComment) toComment() Comment {
	c := Comment{ID: r.ID, Body: r.Body, BodyHTML: r.BodyHTML, CreatedAt: r.CreatedAt, URL: r.URL,
		State: r.State, DiffHunk: r.DiffHunk, CanUpdate: r.ViewerCanUpdate, CanDelete: r.ViewerCanDelete}
	if r.Author != nil {
		c.Author = r.Author.Login
	} else {
		c.Author = "ghost"
	}
	return c
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type rawPR struct {
	Viewer struct {
		Login string `json:"login"`
	} `json:"viewer"`
	Repository struct {
		PullRequest *struct {
			ID          string `json:"id"`
			Number      int    `json:"number"`
			Title       string `json:"title"`
			Body        string `json:"body"`
			BodyHTML    string `json:"bodyHTML"`
			URL         string `json:"url"`
			State       string `json:"state"`
			IsDraft     bool   `json:"isDraft"`
			Author      *struct {
				Login string `json:"login"`
			} `json:"author"`
			BaseRefName string `json:"baseRefName"`
			HeadRefName string `json:"headRefName"`
			HeadRefOid  string `json:"headRefOid"`
			Commits     struct {
				Nodes []struct {
					URL    string `json:"url"`
					Commit struct {
						Oid             string `json:"oid"`
						AbbreviatedOid  string `json:"abbreviatedOid"`
						MessageHeadline string `json:"messageHeadline"`
						Message         string `json:"message"`
						AuthoredDate    string `json:"authoredDate"`
						Author          struct {
							Name string `json:"name"`
							User *struct {
								Login string `json:"login"`
							} `json:"user"`
						} `json:"author"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"commits"`
			Files struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					Path   string `json:"path"`
					Viewed string `json:"viewerViewedState"`
				} `json:"nodes"`
			} `json:"files"`
			ReviewThreads struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					Thread
					Comments struct {
						Nodes []rawComment `json:"nodes"`
					} `json:"comments"`
				} `json:"nodes"`
			} `json:"reviewThreads"`
			Comments struct {
				Nodes []rawComment `json:"nodes"`
			} `json:"comments"`
			Reviews struct {
				Nodes []struct {
					ID              string `json:"id"`
					ViewerDidAuthor bool   `json:"viewerDidAuthor"`
					Commit          *struct {
						Oid string `json:"oid"`
					} `json:"commit"`
				} `json:"nodes"`
			} `json:"reviews"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

// PullRequest loads a PR with all its files and review threads.
func (c *Client) PullRequest(ctx context.Context, owner, name string, number int) (*PR, error) {
	var pr *PR
	var filesCursor, threadsCursor any
	moreFiles, moreThreads := true, true
	for moreFiles || moreThreads {
		var raw rawPR
		vars := map[string]any{"owner": owner, "name": name, "number": number, "files": filesCursor, "threads": threadsCursor}
		if err := c.do(ctx, prQuery, vars, &raw); err != nil {
			return nil, err
		}
		p := raw.Repository.PullRequest
		if p == nil {
			return nil, fmt.Errorf("PR %s/%s#%d not found", owner, name, number)
		}
		if pr == nil {
			pr = &PR{ID: p.ID, Number: p.Number, Title: p.Title, Body: p.Body, BodyHTML: p.BodyHTML, URL: p.URL,
				State: p.State, IsDraft: p.IsDraft, BaseRefName: p.BaseRefName, HeadRefName: p.HeadRefName,
				HeadRefOid: p.HeadRefOid, ViewerLogin: raw.Viewer.Login}
			if p.Author != nil {
				pr.Author = p.Author.Login
			}
			for _, n := range p.Commits.Nodes {
				cm := Commit{Oid: n.Commit.Oid, AbbreviatedOid: n.Commit.AbbreviatedOid, MessageHeadline: n.Commit.MessageHeadline,
					Message: n.Commit.Message, AuthoredDate: n.Commit.AuthoredDate, Author: n.Commit.Author.Name, URL: n.URL}
				if n.Commit.Author.User != nil {
					cm.Author = n.Commit.Author.User.Login
				}
				pr.Commits = append(pr.Commits, cm)
			}
			for _, n := range p.Comments.Nodes {
				pr.Comments = append(pr.Comments, n.toComment())
			}
			for _, n := range p.Reviews.Nodes {
				if n.ViewerDidAuthor {
					pr.Pending = &Review{ID: n.ID}
					if n.Commit != nil {
						pr.Pending.CommitOid = n.Commit.Oid
					}
				}
			}
		}
		if moreFiles {
			for _, n := range p.Files.Nodes {
				pr.Files = append(pr.Files, File{Path: n.Path, Viewed: n.Viewed})
			}
			moreFiles = p.Files.PageInfo.HasNextPage
			filesCursor = p.Files.PageInfo.EndCursor
		}
		if moreThreads {
			for _, n := range p.ReviewThreads.Nodes {
				t := n.Thread
				for _, rc := range n.Comments.Nodes {
					t.Comments = append(t.Comments, rc.toComment())
				}
				pr.Threads = append(pr.Threads, t)
			}
			moreThreads = p.ReviewThreads.PageInfo.HasNextPage
			threadsCursor = p.ReviewThreads.PageInfo.EndCursor
		}
	}
	return pr, nil
}

// HeadOid returns the PR's current head commit, for noticing new pushes.
func (c *Client) HeadOid(ctx context.Context, owner, name string, number int) (string, error) {
	var raw struct {
		Repository struct {
			PullRequest struct {
				HeadRefOid string `json:"headRefOid"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	err := c.do(ctx, `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){headRefOid}}}`,
		map[string]any{"owner": owner, "name": name, "number": number}, &raw)
	return raw.Repository.PullRequest.HeadRefOid, err
}
