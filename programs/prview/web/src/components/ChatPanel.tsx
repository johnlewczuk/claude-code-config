import { forwardRef } from 'preact/compat'
import { useEffect, useImperativeHandle, useRef, useState } from 'preact/hooks'
import { getJSON, stream } from '../api'
import { useReview } from '../ctx'
import type { ChatMessage, Selection } from '../types'
import { markdown } from '../util'

export interface ChatHandle {
  /** Attach selected lines to the next question and focus the box. */
  attach(sel: Selection | null): void
}

/** The Claude panel: one conversation per PR, kept across reloads. */
export const ChatPanel = forwardRef<ChatHandle>(function ChatPanel(_, ref) {
  const rv = useReview()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [sel, setSel] = useState<Selection | null>(null)
  const [question, setQuestion] = useState('')
  const [answer, setAnswer] = useState<string | null>(null)
  const [status, setStatus] = useState<string>('')
  const box = useRef<HTMLTextAreaElement>(null)
  const list = useRef<HTMLDivElement>(null)

  useImperativeHandle(ref, () => ({
    attach(s) {
      setSel(s)
      box.current?.focus()
    },
  }))

  useEffect(() => {
    getJSON<ChatMessage[] | null>(`${rv.api}/chat`)
      .then((m) => setMessages(m ?? []))
      .catch(() => {})
  }, [rv.api])

  useEffect(() => {
    list.current?.scrollTo({ top: list.current.scrollHeight })
  }, [messages, answer, status])

  const send = async () => {
    const q = question.trim()
    if (!q || answer !== null) return
    const mine: ChatMessage = { role: 'user', text: q, selection: sel ?? undefined, at: new Date().toISOString() }
    setMessages((m) => [...m, mine])
    setQuestion('')
    setAnswer('')
    setStatus('Thinking…')
    let text = ''
    let final: ChatMessage | null = null
    try {
      await stream(`${rv.api}/ask`, { question: q, selection: sel }, (event, d) => {
        if (event === 'text') {
          text += d.text
          setAnswer(text)
          setStatus('')
        } else if (event === 'tool') {
          setStatus(d.text)
          if (text && !text.endsWith('\n\n')) {
            text += '\n\n'
            setAnswer(text)
          }
        } else if (event === 'done') final = d
        else if (event === 'failed') final = { role: 'claude', text: d.error, error: true, at: new Date().toISOString() }
      })
    } catch (e) {
      final = { role: 'claude', text: e instanceof Error ? e.message : String(e), error: true, at: new Date().toISOString() }
    }
    setMessages((m) => [...m, final ?? { role: 'claude', text, at: new Date().toISOString() }])
    setAnswer(null)
    setStatus('')
    setSel(null)
  }

  return (
    <aside class="right">
      <div class="h">
        <span>Claude</span>
        <span class="h-src">reads code, can't change or post anything</span>
      </div>
      <div class="chat" ref={list}>
        {messages.length === 0 && answer === null && (
          <div class="meta chat-hint">
            Select lines in the diff and press “Ask Claude”, or just ask about the PR. Claude can read the whole checkout at the PR's head.
          </div>
        )}
        {messages.map((m, i) =>
          m.role === 'user' ? (
            <div key={i} class="me">
              {m.selection && <SelChip sel={m.selection} />}
              {m.text}
            </div>
          ) : (
            <div key={i} class={`cl md ${m.error ? 'error' : ''}`} dangerouslySetInnerHTML={{ __html: markdown(m.text) }} />
          ),
        )}
        {answer !== null && (
          <>
            {answer && <div class="cl md" dangerouslySetInnerHTML={{ __html: markdown(answer) }} />}
            {status && <div class="tool-status">{status}</div>}
          </>
        )}
      </div>
      <div class="ask">
        {sel && (
          <div class="ask-ctx">
            <SelChip sel={sel} />
            <button class="linkish" onClick={() => setSel(null)} title="Don't attach these lines">
              ✕
            </button>
          </div>
        )}
        <textarea
          ref={box}
          class="ta"
          rows={3}
          value={question}
          placeholder={sel ? 'Ask about the selected lines…' : 'Ask about this PR…'}
          onInput={(e) => setQuestion((e.target as HTMLTextAreaElement).value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              send()
            }
          }}
        />
        <div class="row">
          <span class="meta">Enter sends · Shift+Enter new line</span>
          <span class="sp" />
          <button class="btn" onClick={send} disabled={answer !== null || !question.trim()}>
            {answer !== null ? 'Answering…' : 'Send'}
          </button>
        </div>
      </div>
    </aside>
  )
})

function SelChip({ sel }: { sel: Selection }) {
  const rv = useReview()
  // Name the commits the selected lines came from.
  const file = rv.data.files?.find((f) => f.path === sel.path || f.oldPath === sel.path)
  const commits = new Set<string>()
  for (const h of file?.hunks ?? [])
    for (const l of h.lines) {
      const n = sel.side === 'old' ? l.old : l.new
      if (n && n >= sel.start && n <= sel.end && l.commit) commits.add(l.commit)
    }
  const names = [...commits]
    .map((c) => rv.commits.get(c))
    .filter(Boolean)
    .map((c) => `${c!.index}/${c!.total}`)
  return (
    <div class="selchip">
      {sel.path}:{sel.start}
      {sel.end !== sel.start ? `–${sel.end}` : ''}
      {sel.side === 'old' ? ' (old)' : ''}
      {names.length > 0 && ` · from ${names.join(', ')}`}
    </div>
  )
}
