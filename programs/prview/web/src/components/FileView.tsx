import type { ComponentChildren } from 'preact'
import { useMemo, useState } from 'preact/hooks'
import { getJSON } from '../api'
import { formKey, useReview } from '../ctx'
import { languageFor } from '../highlight'
import {
  fileLines,
  inRange,
  keysForLine,
  rangeSelection,
  refOf,
  refOnSide,
  splitRows,
  threadKey,
  unifiedRows,
  type DLine,
  type Expansion,
  type Gap,
  type Range,
  type Row,
} from '../rows'
import type { PeekAnchor } from '../peek'
import { noteKey, noteRows, sectionButtons, sectionReq } from '../summaries'
import type { DiffFile, LineRef, Note, Section, Side, Thread } from '../types'
import { markdown } from '../util'
import { CommentForm } from './CommentForm'
import { Chip, Code, codeClick, noSelectWithCtrl } from './Code'
import { Peek } from './Peek'
import { SummarizeButton, SummaryNote } from './SummaryNote'
import { SummaryView } from './SummaryView'
import { ThreadView } from './ThreadView'

interface Props {
  file: DiffFile
  index: number
  /** The first file shown, which says so when there are no summaries at all. */
  first: boolean
  viewed: string
  collapsed: boolean
  toggleCollapsed(): void
  setViewed(v: boolean): void
}

const STEP = 20

/** A row of the diff: a line, or the gap row of hidden lines. */
type Item = DLine | Gap

/** A file's Summarize buttons and open notes, by the row they go on. */
interface SumMarks {
  /** The side the sections were read from: the new side, or the old for a deleted file. */
  side: 'new' | 'old'
  buttons: Map<Item, Section>
  notes: Map<Item, Note[]>
  open(s: Section): void
}

export function FileView({ file, index, first, viewed, collapsed, toggleCollapsed, setViewed }: Props) {
  const rv = useReview()
  const lines = useMemo(() => fileLines(file), [file])
  const lang = useMemo(() => languageFor(file.path), [file.path])
  const [exp, setExp] = useState<Expansion>(new Map())
  const [source, setSource] = useState<string[] | null>(null)

  // Threads on this file, by the line they hang off; the rest go in the box at the top.
  const { byKey, loose } = useMemo(() => {
    const byKey = new Map<string, Thread[]>()
    const loose: Thread[] = []
    const keys = new Set<string>()
    for (const l of lines) keysForLine(l).forEach((k) => keys.add(k))
    for (const t of rv.threads) {
      if (t.path !== file.path) continue
      const k = t.line ? threadKey(t.diffSide, t.line) : ''
      if (k && keys.has(k)) byKey.set(k, [...(byKey.get(k) ?? []), t])
      else loose.push(t)
    }
    return { byKey, loose }
  }, [rv.threads, lines, file.path])

  const rows = useMemo(() => unifiedRows(file, lines, exp, source), [file, lines, exp, source])
  const binary = file.binary || !file.hunks?.length
  const [sumHidden, setSumHidden] = useState(false)

  // Summaries: none until the page knows them, and never when they are off.
  const fsum = rv.sums.files[file.path]
  const side = file.status === 'deleted' ? 'old' : 'new'
  const sidePath = side === 'old' ? file.oldPath : file.path
  const sections = fsum && !collapsed && !binary ? rv.sectionsOf(sidePath, side) : null
  const { notes, openNote } = rv
  const summaryView = rv.view === 'summary'
  const marks = useMemo((): SumMarks => {
    const fileNotes = [...notes.values()].filter((n) => n.req.path === (n.req.side === 'old' ? file.oldPath : file.path))
    // The Summary view shows the buttons and the sections' notes in its left pane; only other notes stay in the code.
    const sectionNote = (n: Note) => n.req.side === side && !!sections?.some((s) => s.start === n.req.start && s.end === n.req.end)
    const inline = summaryView ? fileNotes.filter((n) => !sectionNote(n)) : fileNotes
    const buttons = sections && !summaryView ? sectionButtons(rows, sections, side) : new Map<number, Section>()
    const item = (i: number): Item => {
      const r = rows[i]
      return r.t === 'gap' ? r.gap : r.line
    }
    const byItem = <T,>(m: Map<number, T>) => new Map([...m].map(([i, v]) => [item(i), v]))
    return {
      side,
      buttons: byItem(buttons),
      notes: byItem(noteRows(rows, buttons, side, inline)),
      open: (s) => openNote(sectionReq(sidePath, side, s)),
    }
  }, [rows, sections, side, sidePath, notes, openNote, file, summaryView])

  const expand = async (gap: Gap, dir: 'down' | 'up' | 'all') => {
    let src = source
    if (!src) {
      try {
        const side = file.status === 'deleted' ? 'old' : 'new'
        const r = await getJSON<{ lines: string[] }>(
          `${rv.api}/lines?side=${side}&path=${encodeURIComponent(file.status === 'deleted' ? file.oldPath : file.path)}`,
        )
        src = r.lines
        setSource(src)
      } catch (e) {
        rv.toast(e instanceof Error ? e.message : String(e))
        return
      }
    }
    setExp((m) => {
      const n = new Map(m)
      const cur = n.get(gap.index) ?? { top: 0, bottom: 0 }
      const size = (gap.end === Infinity ? src!.length : gap.end) - gap.start + 1
      if (dir === 'all') n.set(gap.index, { top: cur.top + size, bottom: cur.bottom })
      else if (dir === 'down') n.set(gap.index, { ...cur, top: cur.top + STEP })
      else n.set(gap.index, { ...cur, bottom: cur.bottom + STEP })
      return n
    })
  }

  const threadCount = rv.threads.filter((t) => t.path === file.path).length
  const summary = !sumHidden && <FileSummary path={file.path} first={first} hide={() => setSumHidden(true)} />
  const table = { file, index, lines, lang, byKey, marks, expand }

  return (
    <section class="file" id={`file-${index}`}>
      <div class="fh">
        <button class="fold" onClick={toggleCollapsed} aria-label={collapsed ? 'Show file' : 'Hide file'}>
          {collapsed ? '▸' : '▾'}
        </button>
        <span class="fpath" onClick={toggleCollapsed}>
          {file.status === 'renamed' ? (
            <>
              <span class="meta">{file.oldPath} →</span> {file.path}
            </>
          ) : (
            file.path
          )}
        </span>
        {file.status !== 'modified' && <span class={`tag st-${file.status}`}>{file.status}</span>}
        {threadCount > 0 && <span class="meta">💬 {threadCount}</span>}
        <span class="sp" />
        <span class="add-n">+{file.additions}</span> <span class="del-n">−{file.deletions}</span>
        {sumHidden && fsum && !collapsed && (
          <button class="linkish" onClick={() => setSumHidden(false)} title="Show this file's summary">
            ⌄ summary
          </button>
        )}
        {viewed === 'DISMISSED' && <span class="tag warn">changed since viewed</span>}
        <label class="viewed">
          <input type="checkbox" checked={viewed === 'VIEWED'} onChange={(e) => setViewed((e.target as HTMLInputElement).checked)} /> Viewed
        </label>
      </div>
      {!collapsed && (
        <>
          {(!summaryView || binary) && summary}
          {loose.length > 0 && <LooseThreads threads={loose} />}
          {binary ? (
            <div class="empty">{file.binary ? 'Binary file not shown.' : 'No text changes (mode or rename only).'}</div>
          ) : summaryView ? (
            <SummaryView
              rows={rows}
              sections={sections}
              side={side}
              sidePath={sidePath}
              open={marks.open}
              summary={summary}
              code={(part) => <UnifiedTable {...table} rows={part} />}
            />
          ) : rv.view === 'split' ? (
            <SplitTable {...table} rows={rows} />
          ) : (
            <UnifiedTable {...table} rows={rows} />
          )}
        </>
      )}
    </section>
  )
}

interface TableProps {
  file: DiffFile
  index: number
  rows: Row[]
  lines: DLine[]
  lang: string | null
  byKey: Map<string, Thread[]>
  marks: SumMarks
  expand(gap: Gap, dir: 'down' | 'up' | 'all'): void
}

/** The unified diff of `rows`: all of a file's rows, or one block of the Summary view. */
function UnifiedTable({ file, index, rows, lines, lang, byKey, marks, expand }: TableProps) {
  const rv = useReview()
  const out: ComponentChildren[] = []
  rows.forEach((r, i) => {
    if (r.t === 'gap') {
      out.push(<GapRow key={'g' + i} gap={r.gap} cols={4} expand={expand} button={sectionButton(marks, r.gap)} />)
      out.push(...summaryRows(marks.notes.get(r.gap), 4))
      return
    }
    const l = r.line
    const ref = l.idx >= 0 ? refOf(l) : null
    const sel = inRange(rv.selection, file.path, l)
    const click = (e: MouseEvent) => ref && rv.select(file.path, ref, e.shiftKey)
    out.push(
      <tr
        key={'l' + i}
        class={`${l.kind} ${sel ? 'sel' : ''} ${l.idx < 0 ? 'more' : ''}`}
        data-r={l.kind !== 'del' && l.new ? `${index}:${l.new}` : undefined}
        data-o={l.kind !== 'add' && l.old ? `${index}:${l.old}` : undefined}
      >
        <td class="c">
          <Chip commit={l.commit} />
        </td>
        <td class={`ln ${ref ? 'click' : ''}`} onClick={click}>
          {l.old ?? ''}
        </td>
        <td class={`ln ${ref ? 'click' : ''}`} onClick={click}>
          {l.new ?? ''}
        </td>
        <td
          class="code"
          onMouseDown={noSelectWithCtrl}
          onClick={(e) => ctrlClick(rv, file, index, lang, e, l, l.kind === 'del' ? 'old' : 'new')}
        >
          {sectionButton(marks, l)}
          <Code line={l} lang={lang} />
        </td>
      </tr>,
    )
    out.push(...extras(rv, file, index, lines, l, keysForLine(l), byKey, 4, i, marks.notes.get(l)))
  })
  return (
    <table class="diff unified">
      <colgroup>
        <col class="col-c" />
        <col class="col-ln" />
        <col class="col-ln" />
        <col />
      </colgroup>
      <tbody>{out}</tbody>
    </table>
  )
}

function SplitTable({ file, index, rows, lines, lang, byKey, marks, expand }: TableProps) {
  const rv = useReview()
  const out: ComponentChildren[] = []
  // The side whose code cells carry the Summarize buttons.
  const buttonSide: Side = marks.side === 'old' ? 'LEFT' : 'RIGHT'
  splitRows(rows).forEach((r, i) => {
    if (r.t === 'gap') {
      out.push(<GapRow key={'g' + i} gap={r.gap} cols={6} expand={expand} button={sectionButton(marks, r.gap)} />)
      out.push(...summaryRows(marks.notes.get(r.gap), 6))
      return
    }
    const half = (l: DLine | null, side: Side) => {
      if (!l) return [<td class="c empty-cell" />, <td class="ln empty-cell" />, <td class="code empty-cell" />]
      const n = side === 'LEFT' ? l.old : l.new
      const ref: LineRef | null = l.idx >= 0 && n ? refOnSide(l, side) : null
      const sel = inRange(rv.selection, file.path, l, side)
      const kind = l.kind === 'ctx' ? 'ctx' : l.kind
      const click = (e: MouseEvent) => ref && rv.select(file.path, ref, e.shiftKey)
      return [
        <td class={`c ${kind}`}>{l.kind !== 'ctx' && <Chip commit={l.commit} />}</td>,
        <td
          class={`ln ${kind} ${sel ? 'sel' : ''} ${ref ? 'click' : ''}`}
          onClick={click}
          data-r={side === 'RIGHT' && n ? `${index}:${n}` : undefined}
          data-o={side === 'LEFT' && n ? `${index}:${n}` : undefined}
        >
          {n ?? ''}
        </td>,
        <td
          class={`code ${kind} ${sel ? 'sel' : ''} ${l.idx < 0 ? 'more' : ''}`}
          onMouseDown={noSelectWithCtrl}
          onClick={(e) => ctrlClick(rv, file, index, lang, e, l, l.kind === 'del' ? 'old' : 'new')}
        >
          {side === buttonSide && sectionButton(marks, l)}
          <Code line={l} lang={lang} />
        </td>,
      ]
    }
    out.push(
      <tr key={'p' + i}>
        {half(r.left, 'LEFT')}
        {half(r.right, 'RIGHT')}
      </tr>,
    )
    const keys = [...(r.left ? keysForLine(r.left, 'LEFT') : []), ...(r.right ? keysForLine(r.right, 'RIGHT') : [])]
    const anchor = r.right ?? r.left!
    const notes = [...(r.right ? marks.notes.get(r.right) ?? [] : []), ...(r.left && r.left !== r.right ? marks.notes.get(r.left) ?? [] : [])]
    out.push(...extras(rv, file, index, lines, anchor, [...new Set(keys)], byKey, 6, i, notes, r.left))
  })
  return (
    <table class="diff split">
      <colgroup>
        <col class="col-c" />
        <col class="col-ln" />
        <col class="col-half" />
        <col class="col-c" />
        <col class="col-ln" />
        <col class="col-half" />
      </colgroup>
      <tbody>{out}</tbody>
    </table>
  )
}

/**
 * The rows that follow a line: its threads, the peek, its summary notes, the
 * selection's action bar, and any open comment form that ends on it.
 */
function extras(
  rv: ReturnType<typeof useReview>,
  file: DiffFile,
  index: number,
  lines: DLine[],
  l: DLine,
  keys: string[],
  byKey: Map<string, Thread[]>,
  cols: number,
  i: number,
  notes: Note[] | undefined,
  other?: DLine | null,
): ComponentChildren[] {
  const out: ComponentChildren[] = []
  const seen = new Set<string>()
  for (const k of keys)
    for (const t of byKey.get(k) ?? []) {
      if (seen.has(t.id)) continue
      seen.add(t.id)
      out.push(
        <tr key={'t' + t.id} class="thread-row">
          <td colSpan={cols}>
            <ThreadView thread={t} />
          </td>
        </tr>,
      )
    }
  if (peekHangsHere(rv.peek?.anchor, index, l, other))
    out.push(
      <tr key="peek" class="peek-row">
        <td colSpan={cols}>
          <Peek />
        </td>
      </tr>,
    )
  out.push(...summaryRows(notes, cols))
  const endsHere = (r: Range | null) =>
    !!r && r.path === file.path && l.idx >= 0 && (r.end.idx === l.idx || (other != null && other.idx === r.end.idx))
  const sel = rv.selection
  if (endsHere(sel) && !rv.openForms.some((f) => formKey(f) === formKey(sel!))) {
    out.push(
      <tr key={'a' + i} class="thread-row">
        <td colSpan={cols}>
          <SelectionBar file={file} range={sel!} lines={lines} />
        </td>
      </tr>,
    )
  }
  for (const f of rv.openForms)
    if (endsHere(f))
      out.push(
        <tr key={'f' + formKey(f)} class="thread-row">
          <td colSpan={cols}>
            <NewComment range={f} lines={lines} />
          </td>
        </tr>,
      )
  return out
}

/** Ctrl+click on a name in the diff opens the peek window under its line. */
function ctrlClick(
  rv: ReturnType<typeof useReview>,
  file: DiffFile,
  index: number,
  lang: string | null,
  e: MouseEvent,
  l: DLine,
  side: 'new' | 'old',
) {
  const target = codeClick(e, l.text, lang)
  const line = side === 'old' ? l.old : l.new
  if (!target || !line) return
  rv.openPeek(
    { fileIndex: index, idx: l.idx, line: l.idx < 0 ? line : undefined },
    { ...target, path: side === 'old' ? file.oldPath : file.path, side, line },
  )
}

function peekHangsHere(a: PeekAnchor | undefined, index: number, l: DLine, other?: DLine | null) {
  if (!a || 'full' in a || a.fileIndex !== index) return false
  const same = (x: DLine) => (x.idx >= 0 ? x.idx === a.idx : a.line !== undefined && (x.new ?? x.old) === a.line)
  return same(l) || (!!other && same(other))
}

/** The ▸ Summarize button of the section that starts on this row, if one does. */
function sectionButton(marks: SumMarks, at: Item) {
  const s = marks.buttons.get(at)
  return s ? <SummarizeButton section={s} open={marks.open} /> : null
}

/** Rows holding summary notes, under the row they belong to. */
function summaryRows(notes: Note[] | undefined, cols: number): ComponentChildren[] {
  return (notes ?? []).map((n) => (
    <tr key={'n' + noteKey(n.req)} class="note-row">
      <td colSpan={cols}>
        <SummaryNote note={n} />
      </td>
    </tr>
  ))
}

/** The file's summary, under its name bar. */
function FileSummary({ path, first, hide }: { path: string; first: boolean; hide(): void }) {
  const rv = useReview()
  if (rv.sums.off)
    return first ? (
      <div class="fsum meta" title={rv.sums.off}>
        Summaries need the claude command
      </div>
    ) : null
  const s = rv.sums.files[path]
  if (!s) return null
  return (
    <div class="fsum">
      <div class="fsum-text">
        {s.status === 'ready' ? (
          <div class="md" dangerouslySetInnerHTML={{ __html: markdown(s.text) }} />
        ) : s.status === 'skipped' ? (
          <span class="meta">Skipped: {s.text}</span>
        ) : s.status === 'failed' ? (
          <span class="error">
            Could not summarize: {s.error}{' '}
            <button class="linkish" onClick={() => rv.redoSum(path)}>
              Retry
            </button>
          </span>
        ) : (
          <span class="meta">Summarizing…</span>
        )}
      </div>
      <span class="fsum-btns">
        {s.status === 'ready' && (
          <button class="linkish" onClick={() => rv.redoSum(path)} title="Ask Claude to write this summary again">
            ↻ redo
          </button>
        )}
        <button class="linkish" onClick={hide} title="Hide this summary until the page is reloaded">
          ⌃ hide
        </button>
      </span>
    </div>
  )
}

function SelectionBar({ file, range, lines }: { file: DiffFile; range: Range; lines: DLine[] }) {
  const rv = useReview()
  const n = range.end.idx - range.start.idx + 1
  const summarize = () => {
    const s = rangeSelection(range, lines)
    rv.openNote({ ...s, path: s.side === 'old' ? file.oldPath : file.path, name: '' })
    rv.clearSelection()
  }
  return (
    <div class="selbar">
      <span class="meta">{n === 1 ? `Line ${range.end.line}` : `${n} lines`} selected · shift-click another line to select a range</span>
      <span class="sp" />
      <button class="btn green" onClick={() => rv.openForm(range)}>
        Comment
      </button>
      {rv.data.claude && (
        <button class="btn" onClick={() => rv.askClaude(rangeSelection(range, lines))}>
          ✦ Ask Claude
        </button>
      )}
      {rv.sums.files[file.path] && (
        <button class="btn" onClick={summarize} title="Ask Claude what these lines do and what this PR changed in them">
          Summarize
        </button>
      )}
      <button class="btn" onClick={rv.clearSelection}>
        ✕
      </button>
    </div>
  )
}

function NewComment({ range, lines }: { range: Range; lines: DLine[] }) {
  const rv = useReview()
  const key = formKey(range)
  const covered = lines.slice(range.start.idx, range.end.idx + 1)
  const suggestion =
    range.end.side === 'RIGHT' && range.start.side === 'RIGHT' ? covered.filter((l) => l.kind !== 'del').map((l) => l.text) : null
  const label =
    range.start.idx === range.end.idx
      ? `Comment on line ${range.end.side === 'LEFT' ? 'L' : 'R'}${range.end.line}`
      : `Comment on lines ${range.start.side === 'LEFT' ? 'L' : 'R'}${range.start.line} to ${range.end.side === 'LEFT' ? 'L' : 'R'}${range.end.line}`
  return (
    <div class="thread new">
      <div class="thread-label meta">{label}</div>
      <CommentForm
        draftKey={key}
        selection={rangeSelection(range, lines)}
        suggestion={suggestion}
        submitLabel="Add to review"
        onCancel={() => {
          rv.setDraft(key, '')
          rv.closeForm(range)
        }}
        onSubmit={async (body) => {
          const ok = await rv.comment(range, body)
          if (ok) rv.closeForm(range)
          return ok
        }}
      />
    </div>
  )
}

interface GapProps {
  gap: Gap
  cols: number
  expand(g: Gap, d: 'down' | 'up' | 'all'): void
  /** A section's Summarize button, when the section starts in the hidden lines. */
  button: ComponentChildren
}

function GapRow({ gap, cols, expand, button }: GapProps) {
  const size = gap.end === Infinity ? Infinity : gap.end - gap.start + 1
  const small = size <= STEP * 2
  return (
    <tr class="gap">
      <td colSpan={cols}>
        <span class="gap-btns">
          {small ? (
            <button onClick={() => expand(gap, 'all')} title="Show the hidden lines">
              ↕ {Number.isFinite(size) ? `${size} hidden line${size === 1 ? '' : 's'}` : ''}
            </button>
          ) : (
            <>
              {gap.index > 0 && (
                <button onClick={() => expand(gap, 'down')} title={`Show ${STEP} more lines below the code above`}>
                  ↓
                </button>
              )}
              {gap.end !== Infinity && (
                <button onClick={() => expand(gap, 'up')} title={`Show ${STEP} more lines above the code below`}>
                  ↑
                </button>
              )}
              {gap.end === Infinity && (
                <button onClick={() => expand(gap, 'down')} title="Show more lines">
                  ↓ more
                </button>
              )}
            </>
          )}
        </span>
        <span class="gap-header">{gap.header ? `@@ ${gap.header}` : ''}</span>
        {button}
      </td>
    </tr>
  )
}

function LooseThreads({ threads }: { threads: Thread[] }) {
  const [open, setOpen] = useState(threads.some((t) => !t.isResolved))
  return (
    <div class="loose">
      <button class="loose-head" onClick={() => setOpen(!open)}>
        {open ? '▾' : '▸'} {threads.length} outdated thread
        {threads.length === 1 ? '' : 's'} (the code they were written on has changed)
      </button>
      {open &&
        threads.map((t) => (
          <div class="loose-thread" key={t.id}>
            {t.comments?.[0]?.diffHunk && <pre class="hunk-snip">{lastLines(t.comments[0].diffHunk, 6)}</pre>}
            <ThreadView thread={t} />
          </div>
        ))}
    </div>
  )
}

function lastLines(s: string, n: number) {
  return s.split('\n').slice(-n).join('\n')
}
