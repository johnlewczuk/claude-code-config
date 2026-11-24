import { describe, expect, it } from 'vitest'
import { fileLines, unifiedRows, type Row } from './rows'
import { sectionBlocks, type CodeMode } from './svblocks'
import type { DiffFile, Hunk, Line, Section } from './types'

const ctx = (n: number): Line => ({ kind: 'ctx', old: n, new: n, text: '' })
const hunk = (oldStart: number, newStart: number, lines: Line[]): Hunk => ({
  oldStart,
  oldLines: lines.filter((l) => l.kind !== 'add').length,
  newStart,
  newLines: lines.filter((l) => l.kind !== 'del').length,
  header: '',
  lines,
})
const rowsOf = (...hunks: Hunk[]): Row[] => {
  const file: DiffFile = { path: 'a.go', oldPath: 'a.go', status: 'modified', binary: false, additions: 0, deletions: 0, hunks }
  return unifiedRows(file, fileLines(file), new Map(), null)
}
const sec = (name: string, start: number, end: number): Section => ({ name, start, end })
/** Each block as "name: rows", a row written as gap, +new, -old or the line number of a context line. */
const blocks = (rows: Row[], sections: Section[], mode: CodeMode = 'diff') =>
  sectionBlocks(rows, sections, mode).map(
    (b) =>
      `${b.section?.name ?? '-'}: ` +
      b.rows
        .map((r) =>
          r.t === 'gap' ? 'gap' : r.line.kind === 'add' ? `+${r.line.new}` : r.line.kind === 'del' ? `-${r.line.old}` : `${r.line.new}`,
        )
        .join(' '),
  )

// Line 1 kept, old line 2 replaced by new line 2, line 3 kept.
const replaced = () => rowsOf(hunk(1, 1, [ctx(1), { kind: 'del', old: 2, text: '' }, { kind: 'add', new: 2, text: '' }, { kind: 'ctx', old: 3, new: 3, text: '' }]))

describe('sectionBlocks', () => {
  it('splits one hunk between two sections', () => {
    const rows = rowsOf(hunk(1, 1, [ctx(1), ctx(2), ctx(3), ctx(4)]))
    expect(blocks(rows, [sec('A', 1, 2), sec('B', 3, 10)])).toEqual(['A: 1 2', 'B: 3 4 gap'])
  })

  it('puts a removed line at a section boundary with the next section', () => {
    expect(blocks(replaced(), [sec('A', 1, 1), sec('B', 2, 10)])).toEqual(['A: 1', 'B: -2 +2 3 gap'])
  })

  it('puts a gap row with the block after it', () => {
    const rows = rowsOf(hunk(1, 1, [ctx(1), ctx(2)]), hunk(20, 20, [ctx(20), ctx(21)]))
    expect(blocks(rows, [sec('A', 1, 10), sec('B', 11, 30)])).toEqual(['A: 1 2', 'B: gap 20 21 gap'])
  })

  it('leaves out removed lines in new-only mode', () => {
    expect(blocks(replaced(), [sec('A', 1, 1), sec('B', 2, 10)], 'new')).toEqual(['A: 1', 'B: +2 3 gap'])
  })
})
