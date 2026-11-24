# prview

A local web page for reviewing GitHub pull requests, with five things
github.com doesn't give you:

- **Which commit changed each line.** Every added or removed line has a tag
  like `3/7`. Hover it for the full commit message.
- **A reading order.** Files and functions are listed in the order to read
  them: where the change starts, then along the call path, then helpers and
  tests. The order is taken from the PR description's "Suggested reading
  order" if it has one; otherwise Claude writes one.
- **Claude beside the diff.** Select lines and ask about them, or have Claude
  draft a review comment you then edit. Claude can read the whole checkout
  but can't change files, run commands or post anything.
- **Find where a name is defined and used.** Ctrl+click (Cmd+click on a Mac)
  a name in the code. A window opens under the line with the definition on
  the left and a list of uses on the right. Ctrl+click a name inside the
  window to follow it further, and go back with the arrow in its header.
  Double-click a place to open its whole file; the Back button (or your
  browser's) returns to the diff where you were. Go answers are exact when
  `gopls` is installed; other languages are found by name, so a result may
  include other functions with the same name.
- **Summaries.** Claude (Sonnet) writes a short summary of each changed file
  in the background; it shows under the file's name. A Summarize button on
  each function or section, or on the lines you select, explains just that
  part and what the PR changed in it. The Summary view (the third button, next
  to Unified and Split) lists each section's summary beside its code, shown
  as "diff" or "new only". Nothing is added to the PR description.

You can also do a normal review: comment on lines or ranges, suggest changes,
reply, resolve, mark files Viewed (shared with github.com), and submit as
Comment, Approve or Request changes. Draft comments go into GitHub's pending
review and only go out when you submit.

## Use

```sh
prview 482                                   # PR 482 of the repo in this folder
prview owner/repo#482
prview https://github.com/owner/repo/pull/482
```

The first run starts the server in the background on port 7450 (log in
`~/.cache/prview/server.log`) and opens the browser. It prints the link
with the access key; open that link once on any other machine (for example
over Tailscale) and that browser is remembered. The home page there has a
box to paste a PR link.

`prview stop`, or the ⏻ Stop button in the page, shuts the server down.
Running `prview <PR>` again starts it.

`prview serve [-addr :7450]` runs the server in the foreground instead.

Needs `gh` (logged in), `git`, and `claude` for the Claude parts. Set
`PRVIEW_MODEL` to choose the model Claude uses.

## Where things are kept

- `~/.config/prview/key`: the access key. Delete it and restart the server
  to make a new one, which logs out every browser.
- `~/.cache/prview/repos/`: a light copy of each repo (file contents are
  downloaded only when needed).
- `~/.cache/prview/worktrees/`: a checkout per PR, for Claude to read.
- `~/.cache/prview/orders/`, `chats/`: saved reading orders and Claude
  conversations.
- `~/.cache/prview/summaries/`: saved file and section summaries.

## Build

```sh
make          # builds the page (web/) and the Go binary ./prview
make test     # Go tests, type check and page tests
make install  # go install
```

## Safety

The server listens on all interfaces and can post to GitHub as you, so:
every request needs the key (from the link once, then an `HttpOnly`,
`SameSite=Strict` cookie), and every request that changes something must
come from prview's own page (`Origin` must match `Host`). Comments and
approvals are refused when the PR got new commits since the page loaded;
reload first.
