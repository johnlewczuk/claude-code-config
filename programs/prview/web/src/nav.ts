// Working out which name a click is on, grouping lookup results, and the
// address (URL fragment) of a place opened as a whole file.
import { highlightLine } from './highlight'
import type { Place, Spot } from './types'

export interface ClickTarget {
  name: string
  hint: string
  col: number
}

// Colour classes whose text is never a name worth looking up.
const NOT_NAMES = new Set(['comment', 'string', 'keyword', 'built_in', 'literal', 'number', 'meta'])

const ENTITIES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  '#x27': "'",
  '#39': "'",
}

/** The text ranges (UTF-16 offsets, end exclusive) that highlight.js marks as not names. */
function notNameRanges(text: string, lang: string | null): [number, number][] {
  const html = highlightLine(text, lang)
  const out: [number, number][] = []
  const open: { blocked: boolean; start: number }[] = []
  let pos = 0
  let i = 0
  while (i < html.length) {
    const c = html[i]
    if (c === '<') {
      const end = html.indexOf('>', i)
      const tag = html.slice(i + 1, end)
      if (tag.startsWith('/')) {
        const o = open.pop()
        if (o?.blocked) out.push([o.start, pos])
      } else {
        const cls = /class="([^"]*)"/.exec(tag)?.[1] ?? ''
        open.push({
          blocked: cls.split(/\s+/).some((k) => k.startsWith('hljs-') && NOT_NAMES.has(k.slice(5))),
          start: pos,
        })
      }
      i = end + 1
    } else if (c === '&') {
      const end = html.indexOf(';', i)
      const ent = html.slice(i + 1, end)
      if (end > 0 && ent in ENTITIES) {
        pos += ENTITIES[ent].length
        i = end + 1
      } else {
        pos++
        i++
      }
    } else {
      pos++
      i++
    }
  }
  return out
}

const isWord = (ch: string | undefined) => !!ch && /[A-Za-z0-9_]/.test(ch)

/**
 * The name under the character at `offset` (a UTF-16 index into `text`), the
 * name before a `::`, `.` or `->` in front of it, and the name's 1-based
 * column. Null when the click is not on a name.
 */
export function clickTarget(text: string, lang: string | null, offset: number): ClickTarget | null {
  if (!isWord(text[offset])) return null
  let start = offset
  while (isWord(text[start - 1])) start--
  let end = offset + 1
  while (isWord(text[end])) end++
  if (/[0-9]/.test(text[start])) return null
  if (notNameRanges(text, lang).some(([a, b]) => start >= a && start < b)) return null

  let hint = ''
  const before = text.slice(0, start)
  const q = /([A-Za-z_][A-Za-z0-9_]*)(?:::|\.|->)$/.exec(before)
  if (q) hint = q[1]
  return { name: text.slice(start, end), hint, col: start + 1 }
}

export function groupPlaces(places: Place[]): {
  definition: Place[]
  use: Place[]
  comment: Place[]
} {
  const g = {
    definition: [] as Place[],
    use: [] as Place[],
    comment: [] as Place[],
  }
  for (const p of places) g[p.kind].push(p)
  return g
}

/** `#file=<path>&line=<n>`, plus `&side=old` for the version before the PR. */
export function spotToHash(s: Spot): string {
  const path = s.path.split('/').map(encodeURIComponent).join('/')
  return `#file=${path}&line=${s.line}${s.side === 'old' ? '&side=old' : ''}`
}

export function hashToSpot(hash: string): Spot | null {
  const p = new URLSearchParams(hash.replace(/^#/, ''))
  const path = p.get('file')
  const line = Number(p.get('line'))
  if (!path || !Number.isInteger(line) || line < 1) return null
  return { path, line, side: p.get('side') === 'old' ? 'old' : 'new' }
}
