import { useState } from 'preact/hooks'
import { useReview } from '../ctx'
import type { Selection, Thread } from '../types'
import { safeHTML, timeAgo } from '../util'
import { CommentForm } from './CommentForm'

/** One review thread with its comments, reply box and resolve button. */
export function ThreadView({ thread }: { thread: Thread }) {
  const rv = useReview()
  const [open, setOpen] = useState(!thread.isResolved)
  const [replying, setReplying] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const comments = thread.comments ?? []
  const replyKey = 'reply:' + thread.id

  const selection: Selection | null = thread.line
    ? {
        path: thread.path,
        side: thread.diffSide === 'LEFT' ? 'old' : 'new',
        start: thread.startLine ?? thread.line,
        end: thread.line,
      }
    : null
  const asText = () => comments.map((c) => `${c.author}: ${c.body}`).join('\n\n')

  const resolve = async () => {
    setBusy(true)
    await rv.resolve(thread.id, !thread.isResolved)
    setBusy(false)
  }

  if (!open) {
    return (
      <div class="thread folded">
        <button class="linkish" onClick={() => setOpen(true)}>
          ▸ {thread.isResolved ? 'Resolved' : 'Hidden'} thread · {comments[0]?.author}: {comments[0]?.body.slice(0, 80)}
        </button>
      </div>
    )
  }

  return (
    <div class={`thread ${thread.isResolved ? 'resolved' : ''}`}>
      {(thread.isResolved || thread.isOutdated) && (
        <div class="thread-tags">
          {thread.isResolved && (
            <button class="linkish" onClick={() => setOpen(false)}>
              ▾ Resolved
            </button>
          )}
          {thread.isOutdated && <span class="tag">Outdated</span>}
        </div>
      )}
      {comments.map((c) => (
        <div class="cm" key={c.id}>
          <div class="who">
            {c.author} <span>· {timeAgo(c.createdAt)}</span>
            {c.state === 'PENDING' && <span class="pending">Pending</span>}
            <span class="sp" />
            {c.viewerCanUpdate && editing !== c.id && (
              <button class="linkish" onClick={() => setEditing(c.id)}>
                Edit
              </button>
            )}
            {c.viewerCanDelete && c.state === 'PENDING' && <DeleteButton onDelete={() => rv.remove(c.id)} />}
          </div>
          {editing === c.id ? (
            <CommentForm
              draftKey={'edit:' + c.id}
              initial={c.body}
              selection={selection}
              suggestion={null}
              submitLabel="Save"
              onCancel={() => setEditing(null)}
              onSubmit={async (body) => {
                const ok = await rv.edit(c.id, body)
                if (ok) setEditing(null)
                return ok
              }}
            />
          ) : (
            <div class="md" dangerouslySetInnerHTML={{ __html: safeHTML(c.bodyHTML) }} />
          )}
        </div>
      ))}
      <div class="thread-foot">
        {replying ? (
          <CommentForm
            draftKey={replyKey}
            selection={selection}
            thread={asText()}
            suggestion={null}
            submitLabel="Add reply to review"
            onCancel={() => {
              rv.setDraft(replyKey, '')
              setReplying(false)
            }}
            onSubmit={async (body) => {
              const ok = await rv.reply(thread.id, body)
              if (ok) setReplying(false)
              return ok
            }}
          />
        ) : (
          <div class="row">
            {thread.viewerCanReply && (
              <button class="btn reply-btn" onClick={() => setReplying(true)}>
                {rv.draft(replyKey) ? 'Continue your reply…' : 'Reply…'}
              </button>
            )}
            <span class="sp" />
            {(thread.isResolved ? thread.viewerCanUnresolve : thread.viewerCanResolve) && (
              <button class="btn" disabled={busy} onClick={resolve}>
                {thread.isResolved ? 'Unresolve' : 'Resolve'}
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

/** Delete asks once more in the page itself (no browser pop-up). */
function DeleteButton({ onDelete }: { onDelete(): Promise<boolean> }) {
  const [sure, setSure] = useState(false)
  if (!sure)
    return (
      <button class="linkish" onClick={() => setSure(true)}>
        Delete
      </button>
    )
  return (
    <span class="confirm">
      Delete this comment?{' '}
      <button class="linkish danger" onClick={() => onDelete()}>
        Yes
      </button>{' '}
      <button class="linkish" onClick={() => setSure(false)}>
        No
      </button>
    </span>
  )
}
