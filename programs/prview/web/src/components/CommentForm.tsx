import { useEffect, useRef, useState } from 'preact/hooks'
import { useReview } from '../ctx'
import type { Selection } from '../types'

interface Props {
  draftKey: string
  initial?: string
  selection: Selection | null
  /** The selected new-side lines, for "Suggest change"; null when not possible. */
  suggestion: string[] | null
  /** The thread so far, for Claude when replying. */
  thread?: string
  submitLabel: string
  onSubmit(body: string): Promise<boolean>
  onCancel(): void
}

/** A comment box with "Draft with Claude" and "Suggest change". */
export function CommentForm({ draftKey, initial, selection, suggestion, thread, submitLabel, onSubmit, onCancel }: Props) {
  const rv = useReview()
  const text = rv.draft(draftKey) || (initial ?? '')
  const set = (t: string) => rv.setDraft(draftKey, t)
  const [busy, setBusy] = useState(false)
  const [asking, setAsking] = useState(false)
  const [instruction, setInstruction] = useState('')
  const [writing, setWriting] = useState(false)
  const ta = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    ta.current?.focus()
  }, [])

  const submit = async () => {
    if (!text.trim() || busy) return
    setBusy(true)
    const ok = await onSubmit(text)
    setBusy(false)
    if (ok) set('')
  }

  const suggest = () => {
    if (!suggestion) return
    const block = '```suggestion\n' + suggestion.join('\n') + '\n```\n'
    set(text ? text.replace(/\s*$/, '\n\n') + block : block)
    setTimeout(() => ta.current?.focus(), 0)
  }

  const draft = async () => {
    if (!instruction.trim()) return
    setWriting(true)
    const before = text
    const result = await rv.draftWithClaude({ instruction, selection, thread, current: before }, (t) => set(t))
    setWriting(false)
    if (result !== null) {
      set(result)
      setAsking(false)
      setInstruction('')
    } else {
      set(before)
    }
  }

  return (
    <div class="form">
      <textarea
        ref={ta}
        class="ta"
        value={text}
        placeholder="Leave a comment (Markdown). Ctrl+Enter adds it."
        onInput={(e) => set((e.target as HTMLTextAreaElement).value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
            e.preventDefault()
            submit()
          }
        }}
        disabled={writing}
        rows={Math.min(14, Math.max(3, text.split('\n').length + 1))}
      />
      {asking && (
        <div class="draft-ask">
          <input
            class="mock-input"
            value={instruction}
            placeholder='What should the comment say? e.g. "ask why the cap is 5"'
            onInput={(e) => setInstruction((e.target as HTMLInputElement).value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') draft()
              if (e.key === 'Escape') setAsking(false)
            }}
            disabled={writing}
            autoFocus
          />
          <button class="btn" onClick={draft} disabled={writing || !instruction.trim()}>
            {writing ? 'Writing…' : text ? 'Rewrite' : 'Write'}
          </button>
        </div>
      )}
      <div class="row">
        {rv.data.claude && !asking && (
          <button class="btn" onClick={() => setAsking(true)} title="Claude writes the comment; you edit it before adding it">
            ✦ Draft with Claude
          </button>
        )}
        {suggestion && (
          <button class="btn" onClick={suggest} title="Propose replacement code the author can apply">
            ± Suggest change
          </button>
        )}
        <span class="sp" />
        <button class="btn" onClick={onCancel}>
          Cancel
        </button>
        <button class="btn green" onClick={submit} disabled={busy || writing || !text.trim()}>
          {busy ? 'Adding…' : submitLabel}
        </button>
      </div>
    </div>
  )
}
