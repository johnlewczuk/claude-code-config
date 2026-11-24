import type { ComponentChildren } from 'preact'
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks'
import { useReview } from '../ctx'
import { languageFor } from '../highlight'
import { fetchFile } from '../lines'
import type { DLine } from '../rows'
import { Chip, Code, codeClick, noSelectWithCtrl } from './Code'
import { Peek } from './Peek'
import { SummarizeButton, SummaryNote } from './SummaryNote'
import { noteKey, sectionReq } from '../summaries'

interface Props {
  path: string
  side: 'new' | 'old'
  line: number
  lang: string | null
  onCtrlClick(t: { name: string; hint: string; col: number; line: number }): void
  /** Lines the PR added (by new line number): tinted and tagged with their commit. */
  added?: Map<number, DLine>
  /** Show the peek window under its line, when it is anchored in this view. */
  peekable?: boolean
  /** Where to scroll to instead of centring `line`. */
  scroll?: number
  onScroll?(top: number): void
  /** Give each section of this file a Summarize button and show its open note. */
  sections?: { path: string; side: 'new' | 'old' }
}

/** A whole file with a gutter and syntax colours, the given line marked and centred. */
export function CodeView({ path, side, line, lang, onCtrlClick, added, peekable, scroll, onScroll, sections }: Props) {
  const rv = useReview()
  const [lines, setLines] = useState<string[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const rev = side === 'old' ? rv.data.base : rv.data.head

  useEffect(() => {
    let live = true
    setLines(null)
    setError(null)
    fetchFile(rv.api, side, rev, path).then(
      (l) => live && setLines(l),
      (e) => live && setError(e instanceof Error ? e.message : String(e)),
    )
    return () => {
      live = false
    }
  }, [rv.api, side, rev, path])

  useLayoutEffect(() => {
    const el = box.current
    if (!el || !lines) return
    if (scroll !== undefined) {
      el.scrollTop = scroll
      return
    }
    const row = el.querySelector<HTMLElement>(`[data-line="${line}"]`)
    if (row) el.scrollTop = row.offsetTop - el.clientHeight / 2 + row.offsetHeight / 2
  }, [lines, line])

  const language = lang ?? languageFor(path)
  const found = sections && !rv.sums.off ? rv.sectionsOf(sections.path, sections.side) : null
  const starts = useMemo(() => new Map((found ?? []).map((s) => [s.start, s])), [found])
  const { openNote } = rv
  const rows = useMemo(
    () =>
      (lines ?? []).map((text, i) => {
        const n = i + 1
        const a = added?.get(n)
        const kind = a ? 'add' : 'ctx'
        return (
          <tr key={n} data-line={n} class={`${kind} ${n === line ? 'cv-mark' : ''}`}>
            {added && (
              <td class="c">
                <Chip commit={a?.commit} />
              </td>
            )}
            <td class="ln">{n}</td>
            <td
              class="code"
              onMouseDown={noSelectWithCtrl}
              onClick={(e) => {
                const t = codeClick(e, text, language)
                if (t) onCtrlClick({ ...t, line: n })
              }}
            >
              {starts.get(n) && (
                <SummarizeButton section={starts.get(n)!} open={(s) => openNote(sectionReq(sections!.path, sections!.side, s))} />
              )}
              <Code line={{ kind, text }} lang={language} />
            </td>
          </tr>
        )
      }),
    [lines, added, line, language, starts, openNote, sections],
  )

  if (error)
    return (
      <div class="empty">
        Could not load {path}: {error}
      </div>
    )
  if (!lines) return <div class="empty">Loading {path}…</div>

  const a = rv.peek?.anchor
  const at = peekable && a && 'full' in a ? a.line : 0
  const peekRow = (
    <tr key="peek" class="peek-row">
      <td colSpan={added ? 3 : 2}>
        <Peek />
      </td>
    </tr>
  )
  const out: ComponentChildren[] = []
  rows.forEach((row, i) => {
    out.push(row)
    if (i + 1 === at) out.push(peekRow)
    const s = starts.get(i + 1)
    const note = s && rv.notes.get(noteKey(sectionReq(sections!.path, sections!.side, s)))
    if (note)
      out.push(
        <tr key={'n' + i} class="note-row">
          <td colSpan={added ? 3 : 2}>
            <SummaryNote note={note} />
          </td>
        </tr>,
      )
  })
  return (
    <div class="cv" ref={box} onScroll={onScroll && ((e) => onScroll((e.currentTarget as HTMLElement).scrollTop))}>
      <table class="diff cv-table">
        <colgroup>
          {added && <col class="col-c" />}
          <col class="col-ln" />
          <col />
        </colgroup>
        <tbody>{out}</tbody>
      </table>
    </div>
  )
}
