import type { CommitInfo } from '../util'
import { timeAgo } from '../util'

interface Props {
  info: CommitInfo
  rect: DOMRect
  keep(): void
  leave(): void
}

/** The popup with a commit's full message, shown when hovering a commit tag. */
export function CommitTip({ info, rect, keep, leave }: Props) {
  const c = info.commit
  const body = unwrap(c.message.slice(c.messageHeadline.length).trim())
  const width = 380
  const left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8))
  const below = rect.bottom + 6
  const style = below + 220 > window.innerHeight ? { left, bottom: window.innerHeight - rect.top + 6, width } : { left, top: below, width }
  return (
    <div class="tip" style={style} onMouseEnter={keep} onMouseLeave={leave}>
      <div class="tip-head">
        <span class="chip" style={{ background: info.color }}>
          {info.index}/{info.total}
        </span>
        <b>{c.messageHeadline}</b>
      </div>
      {body && <pre class="tip-body">{body}</pre>}
      <div class="tip-meta">
        {c.author} · {timeAgo(c.authoredDate)} · <code>{c.abbreviatedOid}</code> ·{' '}
        <a href={c.url} target="_blank" rel="noreferrer">
          open on GitHub ↗
        </a>
      </div>
    </div>
  )
}

/**
 * Commit messages are hard-wrapped at about 72 columns. Rejoin the wrapped
 * lines of each paragraph so the text fits the popup, but keep list items,
 * indented code and trailer lines (Co-Authored-By: ...) on their own lines.
 */
function unwrap(text: string): string {
  const keep = /^(\s|[-*+] |\d+[.)] |[A-Za-z-]+: )/
  return text
    .split(/\n\s*\n/)
    .map((para) =>
      para
        .split('\n')
        .reduce((out, line) => (out && !keep.test(line) ? `${out} ${line.trim()}` : out ? `${out}\n${line}` : line), ''),
    )
    .join('\n\n')
}
