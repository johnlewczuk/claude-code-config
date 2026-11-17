import { useReview, type View } from '../ctx'
import { plural } from '../util'
import { StopButton } from './StopButton'

interface Props {
  pendingCount: number
  hasPending: boolean
  view: View
  setView(v: View): void
  chatOpen: boolean
  toggleChat(): void
  onFinish(): void
}

export function TopBar({ pendingCount, hasPending, view, setView, chatOpen, toggleChat, onFinish }: Props) {
  const { data } = useReview()
  const pr = data.pr
  const n = pr.commits?.length ?? 0
  return (
    <header class="top">
      <div class="top-title">
        <a class="home" href="/" title="Open another PR">
          prview
        </a>
        <span class="title">
          {pr.title} <span class="meta">#{pr.number}</span>
        </span>
        {pr.isDraft && <span class="tag">Draft</span>}
        {pr.state !== 'OPEN' && <span class="tag">{pr.state.toLowerCase()}</span>}
      </div>
      <div class="top-meta meta">
        {pr.author} wants to merge {plural(n, 'commit')} into <code>{pr.baseRefName}</code> from <code>{pr.headRefName}</code>
      </div>
      <div class="sp" />
      <div class="seg" role="group" aria-label="Diff view">
        <button class={view === 'unified' ? 'on' : ''} onClick={() => setView('unified')}>
          Unified
        </button>
        <button class={view === 'split' ? 'on' : ''} onClick={() => setView('split')}>
          Split
        </button>
      </div>
      {data.claude && (
        <button class={`btn ${chatOpen ? 'on' : ''}`} onClick={toggleChat} title="Show or hide the Claude panel">
          ✦ Claude
        </button>
      )}
      <a class="btn" href={pr.url} target="_blank" rel="noreferrer">
        Open on GitHub ↗
      </a>
      <button class="btn green" onClick={onFinish}>
        Finish review{hasPending ? ` · ${pendingCount} pending` : ''}
      </button>
      <StopButton />
    </header>
  )
}
