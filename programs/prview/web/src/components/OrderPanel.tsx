import { postJSON } from '../api'
import { useReview } from '../ctx'
import type { DiffFile, OrderState, Step } from '../types'
import { HideButton } from './Rail'

interface Props {
  files: DiffFile[]
  order: OrderState
  setOrder(o: OrderState): void
  viewedOf(path: string): string
  viewedCount: number
  total: number
  currentStep: number | null
  jumpTo(path: string, step?: Step, index?: number): void
  onHide(): void
}

export function OrderPanel({ files, order, setOrder, viewedOf, viewedCount, total, currentStep, jumpTo, onHide }: Props) {
  const { api, data, toast, commits, sums } = useReview()
  const o = order.order
  const steps = o?.steps ?? []
  const groups = o?.groups ?? []

  const realPath = (p: string) => files.find((f) => f.path === p || f.oldPath === p)?.path ?? p
  const mentioned = new Set<string>()
  steps.forEach((s) => mentioned.add(realPath(s.file)))
  groups.forEach((g) => g.files.forEach((f) => mentioned.add(realPath(f))))
  const rest = files.filter((f) => !mentioned.has(f.path))

  const redo = async () => {
    try {
      setOrder(await postJSON<OrderState>(`${api}/order/redo`, {}))
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e))
    }
  }

  /** A file's summary, shown when hovering its entry, once it is written. */
  const summaryOf = (path: string) => {
    const s = sums.files[realPath(path)]
    return s?.status === 'ready' ? s.text : undefined
  }
  const tick = (path: string) => (viewedOf(realPath(path)) === 'VIEWED' ? '✓' : '')
  const pct = total ? Math.round((viewedCount / total) * 100) : 0

  return (
    <nav class="left">
      <div class="h">
        <span>Reading order</span>
        <span class="h-src">
          {o?.source === 'description' ? 'from PR description' : o?.source === 'claude' ? 'written by Claude' : ''}
          {data.claude && order.status !== 'running' && (
            <button class="linkish" onClick={redo} title="Ask Claude to write the order again">
              ↻ redo
            </button>
          )}
          <HideButton side="left" label="reading order" onHide={onHide} />
        </span>
      </div>
      <div class="prog" title={`${pct}%`}>
        <i style={{ width: `${pct}%` }} />
      </div>
      <div class="prog-text">
        {viewedCount} of {total} files viewed
      </div>

      {order.status === 'running' && <div class="order-wait">Claude is working out a reading order…</div>}
      {order.status === 'failed' && <div class="order-wait error">No reading order: {order.error}</div>}

      {steps.map((s, i) => (
        <button
          key={i}
          class={`step ${currentStep === i ? 'cur' : ''}`}
          onClick={() => jumpTo(s.file, s, i)}
          title={summaryOf(s.file)}
        >
          <span class="ck">{tick(s.file)}</span>
          <span class="n">{i + 1}</span>
          <span class="step-body">
            <span class="f">
              {s.file}
              {s.function ? `:${s.function}` : ''}
            </span>
            {s.note && <span class="note">{s.note}</span>}
          </span>
        </button>
      ))}
      {groups.map((g, i) => (
        <div key={'g' + i} class="step group">
          <span class="ck">{g.files.every((f) => viewedOf(realPath(f)) === 'VIEWED') ? '✓' : ''}</span>
          <span class="n">—</span>
          <span class="step-body">
            <span class="note">{g.note}</span>
            {g.files.map((f) => (
              <button key={f} class="rest-file" onClick={() => jumpTo(f)} title={summaryOf(f)}>
                <span class="ck">{tick(f) || '·'}</span>
                <span>{f}</span>
              </button>
            ))}
          </span>
        </div>
      ))}

      {rest.length > 0 && (
        <>
          <div class="h sub">
            <span>{o ? 'The rest' : 'Files'}</span>
          </div>
          {rest.map((f) => (
            <button key={f.path} class="rest-file" onClick={() => jumpTo(f.path)} title={summaryOf(f.path)}>
              <span class="ck">{tick(f.path) || '·'}</span>
              <span>{f.path}</span>
            </button>
          ))}
        </>
      )}

      <div class="h sub">
        <span>Commits</span>
      </div>
      <div class="commit-list">
        {[...commits.values()].map((c) => (
          <a key={c.commit.oid} class="commit-row" href={c.commit.url} target="_blank" rel="noreferrer" title={c.commit.message}>
            <span class="chip" style={{ background: c.color }}>
              {c.index}/{c.total}
            </span>
            <span class="commit-head">{c.commit.messageHeadline}</span>
          </a>
        ))}
      </div>
    </nav>
  )
}
