import type { ComponentChildren } from 'preact'
import { useMemo } from 'preact/hooks'
import { useReview } from '../ctx'
import type { Row } from '../rows'
import { noteKey, sectionReq } from '../summaries'
import { sectionBlocks } from '../svblocks'
import type { Section } from '../types'
import { SummarizeButton, SummaryNote } from './SummaryNote'

interface Props {
  rows: Row[]
  /** The file's sections; null while loading or when there are no summaries. */
  sections: Section[] | null
  /** The side the sections were read from: the new side, or the old for a deleted file. */
  side: 'new' | 'old'
  sidePath: string
  open(s: Section): void
  /** The file's summary box. */
  summary: ComponentChildren
  /** The code of some of the rows, as a unified diff table. */
  code(rows: Row[]): ComponentChildren
}

/**
 * A file as notes in a left pane beside its code: the file summary at the
 * top, then one block per section with its note or Summarize button level
 * with its rows. Each block is as tall as the taller side.
 */
export function SummaryView({ rows, sections, side, sidePath, open, summary, code }: Props) {
  const rv = useReview()
  // A deleted file has no new side to show.
  const mode = side === 'old' ? 'diff' : rv.codeMode
  const blocks = useMemo(() => sectionBlocks(rows, sections ?? [], mode, side), [rows, sections, mode, side])
  return (
    <div class="sv">
      <div class="sv-row sv-head">
        <div class="sv-left">Summary</div>
        <div class="sv-right">
          Code
          {side === 'new' && (
            <span class="seg" role="group" aria-label="Code shown">
              <button class={mode === 'diff' ? 'on' : ''} onClick={() => rv.setCodeMode('diff')} title="Removed and added lines">
                diff
              </button>
              <button class={mode === 'new' ? 'on' : ''} onClick={() => rv.setCodeMode('new')} title="The code as it is after this PR">
                new only
              </button>
            </span>
          )}
        </div>
      </div>
      <div class="sv-row">
        <div class="sv-left">{summary}</div>
        <div class="sv-right" />
      </div>
      {blocks.map((b, k) => {
        const note = b.section && rv.notes.get(noteKey(sectionReq(sidePath, side, b.section)))
        return (
          <div class="sv-row sv-block" key={k}>
            <div class="sv-left">
              {note ? <SummaryNote note={note} /> : b.section && <SummarizeButton section={b.section} open={open} />}
            </div>
            <div class="sv-right">{code(b.rows)}</div>
          </div>
        )
      })}
    </div>
  )
}
