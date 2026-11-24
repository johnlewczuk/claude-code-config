import { describe, expect, it } from 'vitest'
import { fileLines, rangeSelection, refOf, splitRows, stepTarget, unifiedRows, type Expansion } from './rows'
import type { DiffFile } from './types'

// A file with lines 1-20 where line 5 changed and line 15 was removed.
const file: DiffFile = {
  path: 'a.go',
  oldPath: 'a.go',
  status: 'modified',
  binary: false,
  additions: 1,
  deletions: 2,
  hunks: [
    {
      oldStart: 4, oldLines: 3, newStart: 4, newLines: 3, header: 'func A()',
      lines: [
        { kind: 'ctx', old: 4, new: 4, text: 'four' },
        { kind: 'del', old: 5, text: 'five' },
        { kind: 'add', new: 5, text: 'FIVE', commit: 'c1' },
        { kind: 'ctx', old: 6, new: 6, text: 'six' },
      ],
    },
    {
      oldStart: 14, oldLines: 3, newStart: 14, newLines: 2, header: 'func B()',
      lines: [
        { kind: 'ctx', old: 14, new: 14, text: 'fourteen' },
        { kind: 'del', old: 15, text: 'fifteen' },
        { kind: 'ctx', old: 16, new: 15, text: 'sixteen' },
      ],
    },
  ],
}
const source = Array.from({ length: 19 }, (_, i) => `line ${i + 1}`)
const lines = fileLines(file)
const kinds = (rows: ReturnType<typeof unifiedRows>) =>
  rows.map((r) => (r.t === 'gap' ? `gap ${r.gap.start}-${r.gap.end}` : `${r.line.kind} ${r.line.old ?? ''}/${r.line.new ?? ''}`))

describe('unifiedRows', () => {
  it('shows gaps before, between and after hunks, the last one open-ended until the file is fetched', () => {
    expect(kinds(unifiedRows(file, lines, new Map(), null))).toEqual([
      'gap 1-3',
      'ctx 4/4', 'del 5/', 'add /5', 'ctx 6/6',
      'gap 7-13',
      'ctx 14/14', 'del 15/', 'ctx 16/15',
      'gap 16-Infinity',
    ])
  })

  it('knows the last gap once the file is fetched, and drops it when there is nothing after', () => {
    const rows = kinds(unifiedRows(file, lines, new Map(), source))
    expect(rows.at(-1)).toBe('gap 16-19')
    expect(kinds(unifiedRows(file, lines, new Map(), source.slice(0, 15))).at(-1)).toBe('ctx 16/15')
  })

  it('reveals lines from the top and bottom of a gap with the right old numbers', () => {
    const exp: Expansion = new Map([[1, { top: 2, bottom: 1 }]])
    const rows = kinds(unifiedRows(file, lines, exp, source)).slice(5, 10)
    expect(rows).toEqual(['ctx 7/7', 'ctx 8/8', 'gap 9-12', 'ctx 13/13', 'ctx 14/14'])
  })

  it('does not loop forever when asked to reveal from the bottom of an open-ended gap', () => {
    const exp: Expansion = new Map([[2, { top: 0, bottom: 20 }]])
    expect(kinds(unifiedRows(file, lines, exp, null)).at(-1)).toBe('gap 16-Infinity')
  })
})

describe('splitRows', () => {
  it('puts removed lines beside the added lines that replace them', () => {
    const rows = splitRows(unifiedRows(file, lines, new Map(), null))
    const pairs = rows.filter((r) => r.t === 'pair').map((r) => (r.t === 'pair' ? `${r.left?.text ?? '-'}|${r.right?.text ?? '-'}` : ''))
    expect(pairs).toEqual(['four|four', 'five|FIVE', 'six|six', 'fourteen|fourteen', 'fifteen|-', 'sixteen|sixteen'])
  })
})

describe('ranges', () => {
  it('maps a range over removed and added lines to new-side numbers for Claude', () => {
    const r = { path: 'a.go', start: refOf(lines[1]), end: refOf(lines[3]) }
    expect(r.start).toEqual({ side: 'LEFT', line: 5, idx: 1 })
    expect(rangeSelection(r, lines)).toEqual({ path: 'a.go', side: 'new', start: 5, end: 6 })
  })
})

describe('stepTarget', () => {
  const at = (side: 'new' | 'old', n: number) => {
    const l = stepTarget(lines, side, n)
    return l && `${l.kind} ${l.old ?? ''}/${l.new ?? ''}`
  }
  it('lands on the first changed line, not the unchanged lines before it', () => {
    // A function defined on line 2, above the hunk: its change is on line 5.
    expect(at('new', 2)).toBe('del 5/')
    expect(at('new', 4)).toBe('del 5/')
    expect(at('new', 5)).toBe('add /5')
  })
  it('skips to the next hunk when the rest of this one is unchanged', () => {
    expect(at('new', 6)).toBe('del 15/')
  })
  it('counts the old side for steps on removed code', () => {
    expect(at('old', 14)).toBe('del 15/')
  })
  it('falls back to the line itself, then to the last line, when no change follows', () => {
    expect(at('new', 15)).toBe('ctx 16/15')
    expect(at('new', 18)).toBe('ctx 16/15')
  })
})
