import { createContext } from 'preact'
import { useContext } from 'preact/hooks'
import type { Range } from './rows'
import type { LoadPayload, Selection, Thread } from './types'
import type { CommitInfo } from './util'

export type View = 'unified' | 'split'

/** What every part of the review screen can read and do. */
export interface ReviewApi {
  data: LoadPayload
  api: string // /api/pr/owner/repo/n
  commits: Map<string, CommitInfo>
  threads: Thread[]
  view: View

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
  draftWithClaude(req: { instruction: string; selection: Selection | null; thread?: string; current?: string }, onText: (t: string) => void): Promise<string | null>

  toast(msg: string): void
}

export const ReviewCtx = createContext<ReviewApi>(null as unknown as ReviewApi)

export function useReview() {
  return useContext(ReviewCtx)
}

export function formKey(r: Range) {
  return `new:${r.path}:${r.end.side}:${r.end.line}`
}
