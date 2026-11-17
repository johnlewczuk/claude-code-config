import { useState } from 'preact/hooks'
import { ApiError, postJSON } from '../api'
import { useReview } from '../ctx'
import type { ThreadsPayload } from '../types'
import { plural } from '../util'

interface Props {
  pendingCount: number
  hasPending: boolean
  onClose(): void
  onDone(p: ThreadsPayload): void
  onMoved(): void
}

type Event = 'COMMENT' | 'APPROVE' | 'REQUEST_CHANGES'

/** Finish the review: summary plus Comment, Approve or Request changes. */
export function SubmitDialog({ pendingCount, hasPending, onClose, onDone, onMoved }: Props) {
  const rv = useReview()
  const [event, setEvent] = useState<Event>('COMMENT')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [sureDiscard, setSureDiscard] = useState(false)
  const body = rv.draft('summary')
  const own = rv.data.pr.author === rv.data.pr.viewerLogin

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      const p = await postJSON<ThreadsPayload>(`${rv.api}/submit`, { event, body, head: rv.data.head })
      rv.setDraft('summary', '')
      rv.toast('Review submitted.')
      onDone(p)
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) onMoved()
      setError(e instanceof Error ? e.message : String(e))
    }
    setBusy(false)
  }

  const discard = async () => {
    setBusy(true)
    try {
      const p = await postJSON<ThreadsPayload>(`${rv.api}/discard`, {})
      rv.toast('Draft review thrown away.')
      onDone(p)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
    setBusy(false)
  }

  const options: { v: Event; label: string; help: string; disabled?: boolean }[] = [
    { v: 'COMMENT', label: 'Comment', help: 'General feedback without approving.' },
    { v: 'APPROVE', label: 'Approve', help: 'Give your approval to merge.', disabled: own },
    { v: 'REQUEST_CHANGES', label: 'Request changes', help: 'Feedback that must be dealt with before merging.', disabled: own },
  ]

  return (
    <div class="overlay" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div class="dialog" role="dialog" aria-label="Finish your review">
        <div class="dialog-head">
          <b>Finish your review</b>
          <span class="meta">{hasPending ? `${plural(pendingCount, 'draft comment')} will be sent` : 'No draft comments'}</span>
          <span class="sp" />
          <button class="linkish" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>
        <textarea
          class="ta"
          rows={6}
          value={body}
          placeholder="Summary (optional, Markdown)"
          onInput={(e) => rv.setDraft('summary', (e.target as HTMLTextAreaElement).value)}
          autoFocus
        />
        <div class="choices">
          {options.map((o) => (
            <label key={o.v} class={`choice ${o.disabled ? 'disabled' : ''}`}>
              <input type="radio" name="event" checked={event === o.v} disabled={o.disabled} onChange={() => setEvent(o.v)} />
              <span>
                <b>{o.label}</b> <span class="meta">{o.disabled ? "You can't do this on your own PR." : o.help}</span>
              </span>
            </label>
          ))}
        </div>
        {error && <div class="error-box">{error}</div>}
        <div class="row">
          {hasPending &&
            (sureDiscard ? (
              <span class="confirm">
                Throw away all {plural(pendingCount, 'draft comment')}?{' '}
                <button class="linkish danger" onClick={discard} disabled={busy}>
                  Yes, throw away
                </button>{' '}
                <button class="linkish" onClick={() => setSureDiscard(false)}>
                  No
                </button>
              </span>
            ) : (
              <button class="linkish danger" onClick={() => setSureDiscard(true)}>
                Throw away draft review
              </button>
            ))}
          <span class="sp" />
          <button class="btn" onClick={onClose}>
            Cancel
          </button>
          <button class="btn green" onClick={submit} disabled={busy}>
            {busy ? 'Submitting…' : 'Submit review'}
          </button>
        </div>
      </div>
    </div>
  )
}
