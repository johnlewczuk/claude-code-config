import { describe, expect, it } from 'vitest'
import { fileLines, unifiedRows } from './rows'
import { sectionButtons } from './summaries'
import type { DiffFile, Hunk, Section } from './types'

const ctx = (n: number) => ({ kind: 'ctx' as const, old: n, new: n, text: `line ${n}` })
const hunk = (start: number, count: number, header = ''): Hunk => ({
  oldStart: start,
  oldLines: count,
  newStart: start,
  newLines: count,
  header,
  lines: Array.from({ length: count }, (_, i) => ctx(start + i)),
})
const fileOf = (...hunks: Hunk[]): DiffFile => ({
  path: 'a.go',
  oldPath: 'a.go',
  status: 'modified',
  binary: false,
  additions: 0,
  deletions: 0,
  hunks,
})
const rowsOf = (file: DiffFile) => unifiedRows(file, fileLines(file), new Map(), null)
/** Each button as "row: section name", the row given as "gap" or its line number. */
const placed = (file: DiffFile, sections: Section[]) => {
  const rows = rowsOf(file)
  return [...sectionButtons(rows, sections, 'new')].map(([i, s]) => {
    const r = rows[i]
    return `${r.t === 'gap' ? 'gap' : r.line.new}: ${s.name}`
  })
}

describe('sectionButtons', () => {
  it('puts the button on the row of the section start when it is shown', () => {
    const file = fileOf(hunk(10, 6))
    expect(placed(file, [{ name: 'A', start: 12, end: 30 }])).toEqual(['12: A'])
  })

  it('puts the button on the gap row before the first hunk when the section starts above it', () => {
    const file = fileOf(hunk(10, 6, 'func A()'))
    expect(placed(file, [{ name: 'A', start: 3, end: 30 }])).toEqual(['gap: A'])
  })

  it('puts the button on line 1 when the first hunk starts there and there is no gap row', () => {
    const file = fileOf(hunk(1, 5))
    const rows = rowsOf(file)
    expect(rows[0].t).toBe('line')
    expect(placed(file, [{ name: '', start: 1, end: 20 }])).toEqual(['1: '])
  })

  it('gives no button to a section with no shown rows', () => {
    const file = fileOf(hunk(10, 6), hunk(40, 3))
    expect(placed(file, [{ name: 'Hidden', start: 20, end: 30 }])).toEqual([])
  })

  it('gives two sections in one hunk a button each', () => {
    const file = fileOf(hunk(10, 10))
    expect(
      placed(file, [
        { name: 'A', start: 5, end: 13 },
        { name: 'B', start: 14, end: 30 },
      ]),
    ).toEqual(['gap: A', '14: B'])
  })
})
