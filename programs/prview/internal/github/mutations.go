package github

import "context"

// SetViewed marks or unmarks a file as Viewed, the same mark github.com shows.
func (c *Client) SetViewed(ctx context.Context, prID, path string, viewed bool) error {
	m := "unmarkFileAsViewed"
	if viewed {
		m = "markFileAsViewed"
	}
	return c.do(ctx, `mutation($pr:ID!,$path:String!){`+m+`(input:{pullRequestId:$pr,path:$path}){clientMutationId}}`,
		map[string]any{"pr": prID, "path": path}, nil)
}

// StartReview creates my pending review on commitOid and returns its id.
func (c *Client) StartReview(ctx context.Context, prID, commitOid string) (string, error) {
	var raw struct {
		AddPullRequestReview struct {
			PullRequestReview struct {
				ID string `json:"id"`
			} `json:"pullRequestReview"`
		} `json:"addPullRequestReview"`
	}
	err := c.do(ctx, `mutation($pr:ID!,$oid:GitObjectID!){addPullRequestReview(input:{pullRequestId:$pr,commitOID:$oid}){pullRequestReview{id}}}`,
		map[string]any{"pr": prID, "oid": commitOid}, &raw)
	return raw.AddPullRequestReview.PullRequestReview.ID, err
}

// NewThread is a new comment on a line or range.
type NewThread struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line"`
	Side      string `json:"side"` // LEFT (old code) or RIGHT (new code)
	StartLine int    `json:"startLine,omitempty"`
	StartSide string `json:"startSide,omitempty"`
}

// AddThread adds a comment thread to my pending review.
func (c *Client) AddThread(ctx context.Context, reviewID string, t NewThread) error {
	vars := map[string]any{"review": reviewID, "path": t.Path, "body": t.Body, "line": t.Line, "side": t.Side}
	q := `mutation($review:ID!,$path:String!,$body:String!,$line:Int!,$side:DiffSide!){
	  addPullRequestReviewThread(input:{pullRequestReviewId:$review,path:$path,body:$body,line:$line,side:$side,subjectType:LINE}){thread{id}}}`
	if t.StartLine > 0 && t.StartLine != t.Line {
		vars["startLine"], vars["startSide"] = t.StartLine, t.StartSide
		q = `mutation($review:ID!,$path:String!,$body:String!,$line:Int!,$side:DiffSide!,$startLine:Int!,$startSide:DiffSide!){
		  addPullRequestReviewThread(input:{pullRequestReviewId:$review,path:$path,body:$body,line:$line,side:$side,startLine:$startLine,startSide:$startSide,subjectType:LINE}){thread{id}}}`
	}
	return c.do(ctx, q, vars, nil)
}

// Reply adds a reply to a thread, as part of my pending review.
func (c *Client) Reply(ctx context.Context, reviewID, threadID, body string) error {
	return c.do(ctx, `mutation($review:ID!,$thread:ID!,$body:String!){
	  addPullRequestReviewThreadReply(input:{pullRequestReviewId:$review,pullRequestReviewThreadId:$thread,body:$body}){comment{id}}}`,
		map[string]any{"review": reviewID, "thread": threadID, "body": body}, nil)
}

// EditComment changes the text of one of my review comments.
func (c *Client) EditComment(ctx context.Context, commentID, body string) error {
	return c.do(ctx, `mutation($id:ID!,$body:String!){updatePullRequestReviewComment(input:{pullRequestReviewCommentId:$id,body:$body}){clientMutationId}}`,
		map[string]any{"id": commentID, "body": body}, nil)
}

// DeleteComment deletes one of my review comments.
func (c *Client) DeleteComment(ctx context.Context, commentID string) error {
	return c.do(ctx, `mutation($id:ID!){deletePullRequestReviewComment(input:{id:$id}){clientMutationId}}`,
		map[string]any{"id": commentID}, nil)
}

// SetResolved resolves or unresolves a thread.
func (c *Client) SetResolved(ctx context.Context, threadID string, resolved bool) error {
	m := "unresolveReviewThread"
	if resolved {
		m = "resolveReviewThread"
	}
	return c.do(ctx, `mutation($id:ID!){`+m+`(input:{threadId:$id}){clientMutationId}}`, map[string]any{"id": threadID}, nil)
}

// Submit events.
const (
	EventComment        = "COMMENT"
	EventApprove        = "APPROVE"
	EventRequestChanges = "REQUEST_CHANGES"
)

// SubmitReview submits my pending review.
func (c *Client) SubmitReview(ctx context.Context, reviewID, event, body string) error {
	return c.do(ctx, `mutation($id:ID!,$event:PullRequestReviewEvent!,$body:String){
	  submitPullRequestReview(input:{pullRequestReviewId:$id,event:$event,body:$body}){clientMutationId}}`,
		map[string]any{"id": reviewID, "event": event, "body": body}, nil)
}

// Review posts a review in one step, for when there is no pending review
// (for example approving without comments).
func (c *Client) Review(ctx context.Context, prID, commitOid, event, body string) error {
	return c.do(ctx, `mutation($pr:ID!,$oid:GitObjectID!,$event:PullRequestReviewEvent!,$body:String){
	  addPullRequestReview(input:{pullRequestId:$pr,commitOID:$oid,event:$event,body:$body}){clientMutationId}}`,
		map[string]any{"pr": prID, "oid": commitOid, "event": event, "body": body}, nil)
}

// DiscardReview deletes my pending review and its comments.
func (c *Client) DiscardReview(ctx context.Context, reviewID string) error {
	return c.do(ctx, `mutation($id:ID!){deletePullRequestReview(input:{pullRequestReviewId:$id}){clientMutationId}}`,
		map[string]any{"id": reviewID}, nil)
}
