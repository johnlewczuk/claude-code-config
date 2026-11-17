// Turns a file's hunks into rows for the unified and split views, with the
// "show more lines" gaps between hunks. Pure functions, no display code.
import type { DiffFile, Line, LineRef, Side } from './types'

/** A diff line with its place in the file's list of hunk lines. */
export interface DLine extends Line {
  /** Position among the file's hunk lines; -1 for lines shown by "show more". */
  idx: number
}

/** Lines hidden between two hunks (or after the last one). */
export interface Gap {
  index: number // gap before hunk `index`; hunks.length = after the last hunk
  start: number // first hidden new-side line number
  end: number // last hidden new-side line number (Infinity: until end of file, not yet known)
  delta: number // new line number minus old line number inside the gap
  header: string // the next hunk's header, if any
}

export type Row = { t: 'gap'; gap: Gap } | { t: 'line'; line: DLine }
export type SplitRow = { t: 'gap'; gap: Gap } | { t: 'pair'; left: DLine | null; right: DLine | null }

/** How many lines have been revealed at the top and bottom of each gap. */
export type Expansion = Map<number, { top: number; bottom: number }>

/** All hunk lines of a file, numbered for ranges. */
export function fileLines(file: DiffFile): DLine[] {
  const out: DLine[] = []
  for (const h of file.hunks ?? []) for (const l of h.lines) out.push({ ...l, idx: out.length })
  return out
}

/**
 * Unified rows. `source` holds the lines of the file (new side, or old side
 * for a deleted file) once fetched; without it, gaps can't be opened.
 */
export function unifiedRows(file: DiffFile, lines: DLine[], exp: Expansion, source: string[] | null): Row[] {
  const rows: Row[] = []
  const hunks = file.hunks ?? []
  const deleted = file.status === 'deleted'
  let pos = 0
  let prevNewEnd = 0
  let prevOldEnd = 0
  const pushGap = (gap: Gap) => {
    const e = exp.get(gap.index) ?? { top: 0, bottom: 0 }
    const end = gap.end === Infinity ? (source ? source.length : Infinity) : gap.end
    const size = end - gap.start + 1
    if (size <= 0) return
    const top = Math.min(e.top, size)
    // Lines can only be shown up from the bottom once the end is known.
    const bottom = end === Infinity ? 0 : Math.min(e.bottom, size - top)
    const ctx = (n: number): DLine => {
      const text = source?.[n - 1] ?? ''
      return deleted
        ? { kind: 'ctx', old: n, text, idx: -1 }
        : { kind: 'ctx', new: n, old: n - gap.delta, text, idx: -1 }
    }
    for (let n = gap.start; n < gap.start + top; n++) rows.push({ t: 'line', line: ctx(n) })
    if (top + bottom < size) rows.push({ t: 'gap', gap: { ...gap, start: gap.start + top, end: end === Infinity ? Infinity : end - bottom } })
    if (bottom > 0) for (let n = end - bottom + 1; n <= end; n++) rows.push({ t: 'line', line: ctx(n) })
  }
  hunks.forEach((h, i) => {
    const start = deleted ? h.oldStart : h.newStart
    const delta = h.newStart - h.oldStart
    pushGap({ index: i, start: (deleted ? prevOldEnd : prevNewEnd) + 1, end: start - 1, delta, header: h.header })
    for (let k = 0; k < h.lines.length; k++) rows.push({ t: 'line', line: lines[pos++] })
    prevNewEnd = h.newStart + h.newLines - 1
    prevOldEnd = h.oldStart + h.oldLines - 1
  })
  if (hunks.length > 0 && file.status !== 'added') {
    pushGap({ index: hunks.length, start: (deleted ? prevOldEnd : prevNewEnd) + 1, end: Infinity, delta: prevNewEnd - prevOldEnd, header: '' })
  }
  return rows
}

/** Split rows: removed lines on the left beside the added lines that replace them. */
export function splitRows(rows: Row[]): SplitRow[] {
  const out: SplitRow[] = []
  let dels: DLine[] = []
  let adds: DLine[] = []
  const flush = () => {
    const n = Math.max(dels.length, adds.length)
    for (let i = 0; i < n; i++) out.push({ t: 'pair', left: dels[i] ?? null, right: adds[i] ?? null })
    dels = []
    adds = []
  }
  for (const r of rows) {
    if (r.t === 'line' && r.line.kind === 'del') {
      if (adds.length) flush()
      dels.push(r.line)
    } else if (r.t === 'line' && r.line.kind === 'add') {
      adds.push(r.line)
    } else {
      flush()
      out.push(r.t === 'gap' ? r : { t: 'pair', left: r.line, right: r.line })
    }
  }
  flush()
  return out
}

/** The GitHub side and line number for a line in unified view. */
export function refOf(l: DLine): LineRef {
  return l.kind === 'del' ? { side: 'LEFT', line: l.old!, idx: l.idx } : { side: 'RIGHT', line: l.new!, idx: l.idx }
}

/** The ref for a line clicked on one side of the split view. */
export function refOnSide(l: DLine, side: Side): LineRef {
  return { side, line: side === 'LEFT' ? l.old! : l.new!, idx: l.idx }
}

export interface Range {
  path: string
  start: LineRef
  end: LineRef
}

export function inRange(r: Range | null, path: string, l: DLine, side?: Side): boolean {
  if (!r || r.path !== path || l.idx < 0) return false
  if (l.idx < r.start.idx || l.idx > r.end.idx) return false
  if (side && r.start.side === r.end.side && side !== r.start.side) return false
  return true
}

export function threadKey(side: Side, line: number) {
  return `${side}:${line}`
}

/** The keys a thread anchored at this line could have, in unified view. */
export function keysForLine(l: DLine, side?: Side): string[] {
  if (side === 'LEFT') return l.old ? [threadKey('LEFT', l.old)] : []
  if (side === 'RIGHT') return l.new ? [threadKey('RIGHT', l.new)] : []
  const keys: string[] = []
  if (l.kind !== 'add' && l.old) keys.push(threadKey('LEFT', l.old))
  if (l.kind !== 'del' && l.new) keys.push(threadKey('RIGHT', l.new))
  return keys
}

/** Converts a range to what the chat and comment drafts use. */
export function rangeSelection(r: Range, lines: DLine[]) {
  const covered = lines.slice(r.start.idx, r.end.idx + 1)
  const news = covered.filter((l) => l.new).map((l) => l.new!)
  if (news.length) return { path: r.path, side: 'new' as const, start: Math.min(...news), end: Math.max(...news) }
  const olds = covered.filter((l) => l.old).map((l) => l.old!)
  return { path: r.path, side: 'old' as const, start: Math.min(...olds), end: Math.max(...olds) }
}
