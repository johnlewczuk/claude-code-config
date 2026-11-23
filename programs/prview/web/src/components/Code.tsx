// Pieces of a line of code shared by the diff, the peek window and the
// whole-file view: the coloured text, the commit tag, and Ctrl+click.
import { useReview } from '../ctx'
import { highlightLine } from '../highlight'
import { clickTarget, type ClickTarget } from '../nav'
import type { Line } from '../types'

export function Chip({ commit }: { commit?: string }) {
  const rv = useReview()
  if (!commit) return null
  const info = rv.commits.get(commit)
  if (!info) return <span class="chip unknown">{commit.slice(0, 7)}</span>
  return (
    <span
      class="chip"
      style={{ background: info.color }}
      onMouseEnter={(e) => rv.showTip(info, e.currentTarget as HTMLElement)}
      onMouseLeave={rv.hideTip}
    >
      {info.index}/{info.total}
    </span>
  )
}

export function Code({ line, lang }: { line: Pick<Line, 'kind' | 'text'>; lang: string | null }) {
  const mark = line.kind === 'add' ? '+' : line.kind === 'del' ? '−' : ' '
  return (
    <>
      <span class="mark">{mark}</span>
      <span class="src" dangerouslySetInnerHTML={{ __html: highlightLine(line.text, lang) }} />
    </>
  )
}

/** The range covering the character at `index` of the element's text, if there is one. */
function charRange(el: Element, index: number): Range | null {
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
  let seen = 0
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const len = n.textContent?.length ?? 0
    if (index < seen + len) {
      const r = document.createRange()
      r.setStart(n, index - seen)
      r.setEnd(n, index - seen + 1)
      return r
    }
    seen += len
  }
  return null
}

/** Which character of the `.src` text is under the pointer. */
function offsetUnderPointer(src: Element, x: number, y: number): number | null {
  const d = document as any
  let node: Node | null = null
  let off = 0
  if (d.caretPositionFromPoint) {
    const p = d.caretPositionFromPoint(x, y)
    node = p?.offsetNode ?? null
    off = p?.offset ?? 0
  } else if (d.caretRangeFromPoint) {
    const r = d.caretRangeFromPoint(x, y) as Range | null
    node = r?.startContainer ?? null
    off = r?.startOffset ?? 0
  }
  if (!node || !src.contains(node)) return null
  const before = document.createRange()
  before.selectNodeContents(src)
  before.setEnd(node, off)
  const caret = before.toString().length
  // The caret sits between two characters; pick the one the pointer is over.
  for (const c of [caret, caret - 1]) {
    const r = c >= 0 ? charRange(src, c) : null
    if (r && [...r.getClientRects()].some((b) => x >= b.left && x <= b.right && y >= b.top && y <= b.bottom)) return c
  }
  return caret
}

/**
 * For a click on a code cell: null unless Ctrl (or Cmd) was held and a name
 * is under the pointer. Holding Ctrl stops the browser's own click action.
 */
export function codeClick(e: MouseEvent, text: string, lang: string | null): ClickTarget | null {
  if (!e.ctrlKey && !e.metaKey) return null
  e.preventDefault()
  const src = (e.currentTarget as HTMLElement).querySelector('.src')
  if (!src) return null
  const offset = offsetUnderPointer(src, e.clientX, e.clientY)
  return offset === null ? null : clickTarget(text, lang, offset)
}

/** Stops a Ctrl+click from starting a text selection. */
export function noSelectWithCtrl(e: MouseEvent) {
  if (e.ctrlKey || e.metaKey) e.preventDefault()
}
