import { useMemo } from 'preact/hooks'
import { useReview } from '../ctx'
import { spotToHash } from '../nav'
import { fileLines, type DLine } from '../rows'
import type { Spot } from '../types'
import { CodeView } from './CodeView'

interface Props {
  /** The place on show: the last step of the trail. */
  spot: Spot
  trail: Spot[]
}

const label = (s: Spot) => `${s.path.split('/').pop()}:${s.line}`

/** A place opened as the whole file in the middle pane, with the trail back to the diff. */
export function FileFull({ spot, trail }: Props) {
  const rv = useReview()
  const file = useMemo(() => rv.data.files?.find((f) => (spot.side === 'old' ? f.oldPath : f.path) === spot.path), [rv.data.files, spot])
  // On the PR's side of a changed file, the lines the PR added are tinted and tagged with their commit.
  const added = useMemo(() => {
    if (!file || spot.side !== 'new') return undefined
    const m = new Map<number, DLine>()
    for (const l of fileLines(file)) if (l.kind === 'add' && l.new) m.set(l.new, l)
    return m
  }, [file, spot.side])

  const back = (steps: number) => history.go(-steps)
  return (
    <div class="full">
      <div class="full-bar">
        <button class="btn" onClick={() => history.back()}>
          ← Back
        </button>
        <span class="full-trail">
          <button class="linkish" onClick={() => back(trail.length)} title="Back to the diff">
            diff
          </button>
          {trail.map((s, i) => (
            <span key={i}>
              {' › '}
              {i === trail.length - 1 ? (
                <span class="full-here">{label(s)}</span>
              ) : (
                <button class="linkish" onClick={() => back(trail.length - 1 - i)}>
                  {label(s)}
                </button>
              )}
            </span>
          ))}
        </span>
        <span class="sp" />
        <button class="btn" onClick={() => back(trail.length)}>
          Back to diff
        </button>
      </div>
      <div class="full-head">
        <span class="full-path">{spot.path}</span>
        <span class="meta">{spot.side === 'old' ? 'before this PR' : file ? '(changed in this PR)' : ''}</span>
      </div>
      <CodeView
        key={spotToHash(spot)}
        path={spot.path}
        side={spot.side}
        line={spot.line}
        lang={null}
        added={added}
        peekable
        summarize
        onCtrlClick={(t) => rv.openPeek({ full: true, line: t.line }, { ...t, path: spot.path, side: spot.side })}
      />
    </div>
  )
}
