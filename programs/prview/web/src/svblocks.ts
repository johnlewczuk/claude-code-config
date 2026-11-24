// Cuts a file's rows into one block per section for the Summary view, so each
// section's note can sit level with its code. Pure functions, no display code.
import type { Row } from './rows'
import type { Section } from './types'

/** What the Summary view's code pane shows: the whole diff, or only the new side. */
export type CodeMode = 'diff' | 'new'

export interface Block {
  /** null: rows outside every section, or sections not known yet. */
  section: Section | null
  rows: Row[]
}

/**
 * Consecutive shown rows grouped by section, in order. A line belongs to the
 * section holding its number on `side` (the side the sections were read
 * from: old for a deleted file). A line with no number there (a removed line,
 * for the new side) goes with the next line of its hunk that has one, or with
 * the line before it when none follows. A gap row goes with the row after
 * it. In 'new' mode removed lines are left out.
 */
export function sectionBlocks(rows: Row[], sections: Section[], mode: CodeMode, side: 'new' | 'old' = 'new'): Block[] {
  const shown = mode === 'new' ? rows.filter((r) => !(r.t === 'line' && r.line.kind === 'del')) : rows
  const numbered = (r: Row) => (r.t === 'line' ? (side === 'old' ? r.line.old : r.line.new) : undefined)
  // Each row's section; undefined while not yet settled.
  const of: (Section | null | undefined)[] = shown.map((r) => {
    const n = numbered(r)
    return n === undefined ? undefined : sections.find((s) => n >= s.start && n <= s.end) ?? null
  })
  const settledBefore = (i: number) => {
    for (let j = i - 1; j >= 0; j--) if (of[j] !== undefined) return of[j]
    return null
  }
  shown.forEach((r, i) => {
    if (r.t !== 'line' || of[i] !== undefined) return
    let j = i + 1
    while (j < shown.length && shown[j].t === 'line' && of[j] === undefined) j++
    of[i] = j < shown.length && shown[j].t === 'line' ? of[j] : settledBefore(i)
  })
  // Gaps last: the row after a gap is always a line, settled above.
  shown.forEach((r, i) => {
    if (r.t === 'gap') of[i] = i + 1 < shown.length ? of[i + 1] : settledBefore(i)
  })

  const out: Block[] = []
  shown.forEach((r, i) => {
    const section = of[i] ?? null
    const last = out[out.length - 1]
    if (last && last.section === section) last.rows.push(r)
    else out.push({ section, rows: [r] })
  })
  return out
}
