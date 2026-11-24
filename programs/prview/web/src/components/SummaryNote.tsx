import { useReview } from '../ctx'
import { noteKey } from '../summaries'
import type { Note } from '../types'
import { markdown } from '../util'

/** A summary of a section or of selected lines, in a box like the peek window's. */
export function SummaryNote({ note }: { note: Note }) {
  const rv = useReview()
  const { req } = note
  const what = req.name || (req.start === req.end ? `line ${req.start}` : `lines ${req.start}–${req.end}`)
  return (
    <div class="snote">
      <div class="snote-head">
        <span>
          Summary of <span class="snote-name">{what}</span>
          {req.side === 'old' && <span class="meta"> (before this PR)</span>}
        </span>
        <span class="sp" />
        <button class="btn" onClick={() => rv.closeNote(noteKey(req))} title="Close (Esc)" aria-label="Close">
          ✕
        </button>
      </div>
      {note.status === 'running' ? (
        <div class="snote-body meta">Summarizing…</div>
      ) : note.status === 'failed' ? (
        <div class="snote-body error">
          {note.error}{' '}
          <button class="linkish" onClick={() => rv.openNote(req)}>
            Retry
          </button>
        </div>
      ) : (
        <div class="snote-body md" dangerouslySetInnerHTML={{ __html: markdown(note.text) }} />
      )}
    </div>
  )
}
