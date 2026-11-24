import { createContext } from 'preact'
import { useContext } from 'preact/hooks'
import type { Range } from './rows'
import type { PeekActions } from './peek'
import type { SummaryActions } from './summaries'
import type { CodeMode } from './svblocks'
import type { LoadPayload, Selection, Spot, Thread } from './types'
import type { CommitInfo } from './util'

export type View = 'unified' | 'split' | 'summary'

/** What every part of the review screen can read and do. */
export interface ReviewApi extends PeekActions, SummaryActions {
  data: LoadPayload
  api: string // /api/pr/owner/repo/n
  commits: Map<string, CommitInfo>
  threads: Thread[]
  view: View
  /** What the Summary view's code pane shows, for every file. */
  codeMode: CodeMode
  setCodeMode(m: CodeMode): void

  selection: Range | null
  select(path: string, ref: Range['start'], extend: boolean): void
  clearSelection(): void

  openForms: Range[]
  openForm(r: Range): void
  closeForm(r: Range): void

  draft(key: string): string
  setDraft(key: string, text: string): void

  showTip(info: CommitInfo, el: HTMLElement): void
  hideTip(): void

  comment(r: Range, body: string): Promise<boolean>
  reply(threadId: string, body: string): Promise<boolean>
  edit(commentId: string, body: string): Promise<boolean>
  remove(commentId: string): Promise<boolean>
  resolve(threadId: string, resolved: boolean): Promise<boolean>

  askClaude(sel: Selection | null): void
  draftWithClaude(
    req: {
      instruction: string
      selection: Selection | null
      thread?: string
      current?: string
    },
    onText: (t: string) => void,
  ): Promise<string | null>

  /** Shows a place as the whole file in the middle pane, as a new step of the trail. */
  openFully(spot: Spot): void

  toast(msg: string): void
}

export const ReviewCtx = createContext<ReviewApi>(null as unknown as ReviewApi)

export function useReview() {
  return useContext(ReviewCtx)
}

export function formKey(r: Range) {
  return `new:${r.path}:${r.end.side}:${r.end.line}`
}
