// Shapes of what the Go server sends. Kept in step with internal/*.

export type Kind = 'ctx' | 'add' | 'del'

export interface Line {
  kind: Kind
  old?: number
  new?: number
  text: string
  commit?: string
}

export interface Hunk {
  oldStart: number
  oldLines: number
  newStart: number
  newLines: number
  header: string
  lines: Line[]
}

export interface DiffFile {
  path: string
  oldPath: string
  status: 'added' | 'deleted' | 'modified' | 'renamed'
  binary: boolean
  additions: number
  deletions: number
  hunks: Hunk[] | null
}

export interface Commit {
  oid: string
  abbreviatedOid: string
  messageHeadline: string
  message: string
  authoredDate: string
  author: string
  url: string
}

export interface Comment {
  id: string
  author: string
  body: string
  bodyHTML: string
  createdAt: string
  state?: string
  diffHunk?: string
  viewerCanUpdate: boolean
  viewerCanDelete: boolean
  url?: string
}

export type Side = 'LEFT' | 'RIGHT'

export interface Thread {
  id: string
  path: string
  isResolved: boolean
  isOutdated: boolean
  line: number | null
  startLine: number | null
  originalLine: number | null
  originalStartLine: number | null
  diffSide: Side
  startDiffSide: Side | null
  subjectType: string
  viewerCanReply: boolean
  viewerCanResolve: boolean
  viewerCanUnresolve: boolean
  comments: Comment[] | null
}

export interface GHFile {
  path: string
  viewed: 'VIEWED' | 'UNVIEWED' | 'DISMISSED'
}

export interface Review {
  id: string
  commitOid: string
}

export interface PR {
  id: string
  number: number
  title: string
  bodyHTML: string
  body: string
  url: string
  state: string
  isDraft: boolean
  author: string
  baseRefName: string
  headRefName: string
  headRefOid: string
  commits: Commit[] | null
  files: GHFile[] | null
  threads: Thread[] | null
  comments: Comment[] | null
  pending: Review | null
  viewerLogin: string
}

export interface Step {
  file: string
  function: string
  note: string
  side: 'new' | 'old'
  startLine: number
  endLine: number
}

export interface Group {
  note: string
  files: string[]
}

export interface Order {
  source: 'description' | 'claude'
  head: string
  steps: Step[] | null
  groups: Group[] | null
}

export interface OrderState {
  status: 'ready' | 'running' | 'failed' | 'none'
  order?: Order
  error?: string
}

export interface LoadPayload {
  owner: string
  repo: string
  pr: PR
  base: string
  head: string
  files: DiffFile[] | null
  order: OrderState
  claude: boolean
}

export interface ThreadsPayload {
  threads: Thread[] | null
  pending: Review | null
  comments: Comment[] | null
  files: GHFile[] | null
}

/** Code a question or comment is about, as the server's chat wants it. */
export interface Selection {
  path: string
  side: 'new' | 'old'
  start: number
  end: number
}

export interface ChatMessage {
  role: 'user' | 'claude'
  text: string
  selection?: Selection
  error?: boolean
  at: string
}

/** A line the user clicked, in GitHub's terms. */
export interface LineRef {
  side: Side
  line: number
  /** Position in the file's list of diff lines, for ranges. */
  idx: number
}
