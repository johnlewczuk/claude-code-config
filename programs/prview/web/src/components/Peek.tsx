import { useEffect, useRef, useState } from 'preact/hooks'
import { useReview } from '../ctx'
import { groupPlaces } from '../nav'
import type { PeekLevel } from '../peek'
import type { Place } from '../types'
import { CodeView } from './CodeView'

const SOURCE_LABEL = {
  gopls: 'exact (Go)',
  name: 'by name: may include other functions with this name',
}

/** The window under a line: where a name is defined and used, with the code of the chosen place. */
export function Peek() {
  const rv = useReview()
  const box = useRef<HTMLDivElement>(null)
  const [commentsOpen, setCommentsOpen] = useState(false)
  const levels = rv.peek?.levels ?? []
  const level = levels[levels.length - 1]

  // Take the keys when it opens, and bring it into view without pushing the clicked line off screen.
  useEffect(() => {
    box.current?.focus({ preventScroll: true })
    box.current?.scrollIntoView({ block: 'nearest' })
  }, [])

  if (!level) return null
  const places = level.result?.places ?? []
  const groups = groupPlaces(places)
  const chosen: Place | undefined = places[level.chosen]
  const showComments = commentsOpen || chosen?.kind === 'comment'
  const listed = [...groups.definition, ...groups.use, ...(showComments ? groups.comment : [])]

  const openFully = () =>
    chosen &&
    rv.openFully({
      path: chosen.path,
      line: chosen.line,
      side: level.target.side,
    })
  const move = (by: number) => {
    const at = chosen ? listed.indexOf(chosen) : -1
    const next = listed[Math.max(0, Math.min(listed.length - 1, at + by))]
    if (next) rv.choosePlace(places.indexOf(next))
  }

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') rv.closePeek()
    else if (e.key === 'ArrowUp') move(-1)
    else if (e.key === 'ArrowDown') move(1)
    else if (e.key === 'ArrowLeft' && e.altKey) rv.peekBack()
    else if (e.key === 'Enter' && !(e.target instanceof HTMLButtonElement)) openFully()
    else return
    e.preventDefault()
  }

  return (
    <div class="peek" ref={box} tabIndex={-1} onKeyDown={onKeyDown}>
      <div class="peek-head">
        {levels.length > 1 && (
          <button class="btn" onClick={rv.peekBack} title="Back to the previous name (Alt+Left)">
            ←
          </button>
        )}
        <span class="peek-trail">
          {levels.map((l, i) => (
            <span key={l.id} class={i === levels.length - 1 ? 'peek-name' : 'meta'}>
              {i > 0 && ' › '}
              {l.target.name}
            </span>
          ))}
        </span>
        <span class="sp" />
        {level.result && <span class="meta peek-source">{SOURCE_LABEL[level.result.source]}</span>}
        <button class="btn" onClick={rv.closePeek} title="Close (Esc)" aria-label="Close">
          ✕
        </button>
      </div>
      <PeekBody
        level={level}
        chosen={chosen}
        groups={groups}
        showComments={showComments}
        toggleComments={() => setCommentsOpen(!commentsOpen)}
        openFully={openFully}
      />
    </div>
  )
}

interface BodyProps {
  level: PeekLevel
  chosen: Place | undefined
  groups: ReturnType<typeof groupPlaces>
  showComments: boolean
  toggleComments(): void
  openFully(): void
}

function PeekBody({ level, chosen, groups, showComments, toggleComments, openFully }: BodyProps) {
  const rv = useReview()
  if (level.status === 'loading')
    return (
      <div class="peek-note">
        <div class="spinner" />
        Getting the code ready…
        {level.progress && <div class="meta">{level.progress}</div>}
      </div>
    )
  if (level.status === 'failed') return <div class="peek-note error">{level.error}</div>
  const result = level.result!
  if (result.places.length === 0)
    return (
      <div class="peek-note">
        No definition or uses of <code>{level.target.name}</code> found in this PR's version of the repo.
      </div>
    )

  const places = result.places
  const entry = (p: Place) => (
    <div
      key={`${p.path}:${p.line}:${p.col}`}
      class={`peek-item ${p === chosen ? 'chosen' : ''}`}
      onClick={() => rv.choosePlace(places.indexOf(p))}
      onDblClick={openFully}
    >
      <div class="peek-where">
        {p.changed && (
          <span class="peek-changed" title="Changed in this PR">
            ●{' '}
          </span>
        )}
        {p.path}:{p.line}
      </div>
      <div class="peek-text">{p.text.trim()}</div>
    </div>
  )
  const group = (title: string, items: Place[]) =>
    items.length > 0 && (
      <>
        <div class="peek-group">
          {title} ({items.length})
        </div>
        {items.map(entry)}
      </>
    )

  return (
    <div class="peek-cols">
      <div class="peek-code">
        {chosen && (
          <>
            <div class="peek-file meta">
              {chosen.path}:{chosen.line}
              {level.target.side === 'old' && ' (before this PR)'}
            </div>
            <CodeView
              key={`${level.id}:${level.chosen}`}
              path={chosen.path}
              side={level.target.side}
              line={chosen.line}
              lang={null}
              onCtrlClick={rv.drill}
              scroll={level.scroll}
              onScroll={(top) => rv.saveScroll(level.id, top)}
            />
          </>
        )}
      </div>
      <div class="peek-list">
        {group('Definition', groups.definition)}
        {group('Uses', groups.use)}
        {groups.comment.length > 0 && (
          <>
            <button class="peek-group peek-fold" onClick={toggleComments}>
              {showComments ? '▾' : '▸'} Mentioned in comments ({groups.comment.length})
            </button>
            {showComments && groups.comment.map(entry)}
          </>
        )}
        {result.more > 0 && <div class="peek-more meta">{result.more} more not shown</div>}
        <div class="peek-open">
          <button class="btn" onClick={openFully} disabled={!chosen}>
            Open ⤢
          </button>
        </div>
      </div>
    </div>
  )
}
