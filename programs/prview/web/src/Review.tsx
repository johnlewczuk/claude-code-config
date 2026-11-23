import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks'
import { ApiError, getJSON, postJSON, prBase, stream } from './api'
import { ChatPanel, type ChatHandle } from './components/ChatPanel'
import { CommitTip } from './components/CommitTip'
import { Description } from './components/Description'
import { FileFull } from './components/FileFull'
import { FileView } from './components/FileView'
import { OrderPanel } from './components/OrderPanel'
import { usePeek } from './peek'
import { SubmitDialog } from './components/SubmitDialog'
import { TopBar } from './components/TopBar'
import { formKey, ReviewCtx, type ReviewApi, type View } from './ctx'
import { hashToSpot, spotToHash } from './nav'
import { fileLines, type Range } from './rows'
import type { DiffFile, GHFile, LoadPayload, OrderState, Spot, Step, Thread, ThreadsPayload } from './types'
import { commitIndex, loadPref, savePref, type CommitInfo } from './util'

interface Props {
  owner: string
  repo: string
  number: number
}

export function Review({ owner, repo, number }: Props) {
  const api = prBase(owner, repo, number)
  const [data, setData] = useState<LoadPayload | null>(null)
  const [progress, setProgress] = useState<string[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reloading, setReloading] = useState(false)

  const [threads, setThreads] = useState<Thread[]>([])
  const [ghFiles, setGhFiles] = useState<GHFile[]>([])
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [order, setOrder] = useState<OrderState>({ status: 'none' })
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [view, setView] = useState<View>(loadPref<View>('view', 'unified'))
  const [chatOpen, setChatOpen] = useState(loadPref('chat', 'open') === 'open')
  const [moved, setMoved] = useState(false)
  const [submitOpen, setSubmitOpen] = useState(false)
  const [toasts, setToasts] = useState<{ id: number; msg: string }[]>([])
  const [currentStep, setCurrentStep] = useState<number | null>(null)

  const [selection, setSelection] = useState<Range | null>(null)
  const anchor = useRef<Range['start'] | null>(null)
  const [openForms, setOpenForms] = useState<Range[]>([])
  const drafts = useRef(new Map<string, string>())
  const [, bumpDrafts] = useState(0)
  const [tip, setTip] = useState<{ info: CommitInfo; rect: DOMRect } | null>(null)
  const tipTimer = useRef<number>()
  const chat = useRef<ChatHandle>(null)
  const peekState = usePeek(api)
  const { closePeek } = peekState
  const mid = useRef<HTMLElement>(null)
  // The places opened as whole files, oldest first; the last is on show. Empty: the diff.
  const [trail, setTrail] = useState<Spot[]>([])
  const spot = trail[trail.length - 1] ?? null
  const trailNow = useRef(trail)
  trailNow.current = trail
  // What to do once the diff is showing again after Back.
  const afterBack = useRef<{ scroll?: number; then?: () => void } | null>(null)
  // The head the page shows; a reload that brings a new one drops what was shown of the old.
  const shownHead = useRef<string | null>(null)

  const toast = useCallback((msg: string) => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t, { id, msg }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 6000)
  }, [])

  // Load (and reload) the PR, streaming progress.
  const load = useCallback(async () => {
    setProgress([])
    setLoadError(null)
    let failed: string | null = null
    try {
      await stream(`${api}/load`, undefined, (event, d) => {
        if (event === 'progress') setProgress((p) => [...p.slice(-30), d.message])
        else if (event === 'failed') failed = d.error
        else if (event === 'ready') {
          const p = d as LoadPayload
          if (shownHead.current && shownHead.current !== p.head) {
            // Places in the peek and the whole-file view are line numbers of the old head.
            closePeek()
            if (trailNow.current.length) history.go(-trailNow.current.length)
          }
          shownHead.current = p.head
          setData(p)
          setThreads(p.pr.threads ?? [])
          setGhFiles(p.pr.files ?? [])
          setPendingId(p.pr.pending?.id ?? null)
          setOrder(p.order)
          setCollapsed(new Set((p.pr.files ?? []).filter((f) => f.viewed === 'VIEWED').map((f) => f.path)))
          setMoved(false)
          setSelection(null)
          document.title = `${p.pr.title} · #${p.pr.number} · prview`
        }
      })
    } catch (e) {
      failed = String(e instanceof Error ? e.message : e)
    }
    if (failed) setLoadError(failed)
  }, [api, closePeek])

  useEffect(() => {
    load()
  }, [load])

  // Back, forward and a reload with a place in the address: the history entry says which trail is on show.
  useEffect(() => {
    const saved: Spot[] | undefined = history.state?.trail
    const first = hashToSpot(location.hash)
    if (saved?.length) {
      // The page was reloaded on a whole file: history already holds the trail, with the diff beneath.
      setTrail(saved)
    } else if (first) {
      // Put the diff underneath, so Back leaves the place for the diff first.
      history.replaceState({ trail: [] }, '', location.pathname + location.search)
      history.pushState({ trail: [first] }, '', location.pathname + location.search + spotToHash(first))
      setTrail([first])
    }
    const onPop = (e: PopStateEvent) => {
      const fromHash = hashToSpot(location.hash)
      const t: Spot[] = e.state?.trail ?? (fromHash ? [fromHash] : [])
      afterBack.current = t.length === 0 ? { scroll: e.state?.scroll, then: afterBack.current?.then } : null
      closePeek()
      setTrail(t)
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [closePeek])

  // Back at the diff: put the scroll where it was, or carry on to the jump that asked for the diff.
  useLayoutEffect(() => {
    const a = afterBack.current
    if (spot || !a) return
    afterBack.current = null
    const scroller = scrollerOf(mid.current)
    if (a.then) a.then()
    else if (a.scroll !== undefined && scroller) scroller.scrollTop = a.scroll
  }, [spot])

  // Poll for the reading order while Claude writes it.
  useEffect(() => {
    if (order.status !== 'running') return
    const t = setInterval(async () => {
      try {
        const o = await getJSON<OrderState>(`${api}/order`)
        if (o.status !== 'running') setOrder(o)
      } catch {
        // try again next tick
      }
    }, 3000)
    return () => clearInterval(t)
  }, [order.status, api])

  // Notice new pushes.
  useEffect(() => {
    if (!data) return
    const t = setInterval(async () => {
      try {
        const { head } = await getJSON<{ head: string }>(`${api}/head`)
        if (head && head !== data.head) setMoved(true)
      } catch {
        // offline for a moment; try again later
      }
    }, 60000)
    return () => clearInterval(t)
  }, [data, api])

  const reload = useCallback(async () => {
    setReloading(true)
    await load()
    setReloading(false)
  }, [load])

  const commits = useMemo(() => commitIndex(data?.pr.commits ?? []), [data])
  const files = data?.files ?? []

  const applyThreads = (p: ThreadsPayload) => {
    setThreads(p.threads ?? [])
    setPendingId(p.pending?.id ?? null)
    if (p.files) setGhFiles(p.files)
  }

  // Runs a review action; returns whether it worked.
  const act = useCallback(
    async (path: string, body: unknown): Promise<boolean> => {
      try {
        applyThreads(await postJSON<ThreadsPayload>(`${api}/${path}`, body))
        return true
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) setMoved(true)
        toast(e instanceof Error ? e.message : String(e))
        return false
      }
    },
    [api, toast],
  )

  const reviewApi: ReviewApi | null = data && {
    data,
    api,
    commits,
    threads,
    view,
    selection,
    select(path, ref, extend) {
      if (extend && anchor.current && selection?.path === path) {
        const a = anchor.current
        setSelection(a.idx <= ref.idx ? { path, start: a, end: ref } : { path, start: ref, end: a })
        return
      }
      if (
        selection &&
        selection.path === path &&
        selection.start.idx === ref.idx &&
        selection.end.idx === ref.idx &&
        selection.start.side === ref.side
      ) {
        setSelection(null)
        anchor.current = null
        return
      }
      anchor.current = ref
      setSelection({ path, start: ref, end: ref })
    },
    clearSelection() {
      setSelection(null)
      anchor.current = null
    },
    openForms,
    openForm(r) {
      setOpenForms((f) => (f.some((x) => formKey(x) === formKey(r)) ? f : [...f, r]))
      setSelection(null)
    },
    closeForm(r) {
      setOpenForms((f) => f.filter((x) => formKey(x) !== formKey(r)))
    },
    draft: (key) => drafts.current.get(key) ?? '',
    setDraft(key, text) {
      if (text) drafts.current.set(key, text)
      else drafts.current.delete(key)
      bumpDrafts((n) => n + 1)
    },
    showTip(info, el) {
      clearTimeout(tipTimer.current)
      setTip({ info, rect: el.getBoundingClientRect() })
    },
    hideTip() {
      clearTimeout(tipTimer.current)
      tipTimer.current = window.setTimeout(() => setTip(null), 250)
    },
    comment: (r, body) =>
      act('comment', {
        path: r.path,
        body,
        line: r.end.line,
        side: r.end.side,
        startLine: r.start.idx !== r.end.idx ? r.start.line : undefined,
        startSide: r.start.idx !== r.end.idx ? r.start.side : undefined,
        head: data.head,
      }),
    reply: (threadId, body) => act('reply', { threadId, body }),
    edit: (commentId, body) => act('edit', { commentId, body }),
    remove: (commentId) => act('delete', { commentId }),
    resolve: (threadId, resolved) => act('resolve', { threadId, resolved }),
    askClaude(sel) {
      if (!chatOpen) {
        setChatOpen(true)
        savePref('chat', 'open')
      }
      setTimeout(() => chat.current?.attach(sel), 0)
    },
    async draftWithClaude(req, onText) {
      let text = ''
      let result: string | null = null
      try {
        await stream(`${api}/draft`, req, (event, d) => {
          if (event === 'text') {
            text += d.text
            onText(text)
          } else if (event === 'done') result = d.text
          else if (event === 'failed') toast(d.error)
        })
      } catch (e) {
        toast(e instanceof Error ? e.message : String(e))
      }
      return result
    },
    ...peekState,
    openFully(s) {
      // Remember where the diff was scrolled to, so Back lands there.
      if (!trailNow.current.length) history.replaceState({ trail: [], scroll: scrollerOf(mid.current)?.scrollTop }, '')
      const next = [...trailNow.current, s]
      history.pushState({ trail: next }, '', location.pathname + location.search + spotToHash(s))
      closePeek()
      setTrail(next)
    },
    toast,
  }

  if (!data) {
    return (
      <div class="loading">
        <h1>
          {owner}/{repo}#{number}
        </h1>
        {loadError ? (
          <div class="error-box">
            <p>{loadError}</p>
            <button class="btn" onClick={() => load()}>
              Try again
            </button>
          </div>
        ) : (
          <div class="progress-log">
            <div class="spinner" />
            {progress.slice(-6).map((p, i) => (
              <div key={i} class="progress-line">
                {p}
              </div>
            ))}
          </div>
        )}
      </div>
    )
  }

  const viewedCount = ghFiles.filter((f) => f.viewed === 'VIEWED').length
  const viewedOf = (path: string) => ghFiles.find((f) => f.path === path)?.viewed ?? 'UNVIEWED'
  const pendingCount = threads.reduce((n, t) => n + (t.comments ?? []).filter((c) => c.state === 'PENDING').length, 0)
  const ordered = orderFiles(files, order)

  const setViewed = async (path: string, viewed: boolean) => {
    const before = ghFiles
    setGhFiles((fs) => fs.map((f) => (f.path === path ? { ...f, viewed: viewed ? 'VIEWED' : 'UNVIEWED' } : f)))
    setCollapsed((c) => {
      const n = new Set(c)
      viewed ? n.add(path) : n.delete(path)
      return n
    })
    try {
      await postJSON(`${api}/viewed`, { path, viewed })
    } catch (e) {
      setGhFiles(before)
      toast(e instanceof Error ? e.message : String(e))
    }
  }

  const toggleCollapsed = (path: string) =>
    setCollapsed((c) => {
      const n = new Set(c)
      n.has(path) ? n.delete(path) : n.add(path)
      return n
    })

  const jumpTo = (path: string, step?: Step, stepIndex?: number) => {
    if (!spot) return showInDiff(path, step, stepIndex)
    // Go back to the diff first, then jump once it is showing.
    afterBack.current = { then: () => showInDiff(path, step, stepIndex) }
    history.go(-trail.length)
  }

  /** Scrolls the diff to a file, or to a reading-order step in it, and flashes it. */
  const showInDiff = (path: string, step?: Step, stepIndex?: number) => {
    if (stepIndex !== undefined) setCurrentStep(stepIndex)
    const fi = files.findIndex((f) => f.path === path || f.oldPath === path)
    if (fi < 0) return
    const file = files[fi]
    if (collapsed.has(file.path)) toggleCollapsed(file.path)
    setTimeout(() => {
      let el: Element | null = null
      if (step && step.startLine > 0) {
        const lines = fileLines(file)
        const target = nearestLine(lines, step.side, step.startLine)
        if (target) el = document.querySelector(`[data-${step.side === 'old' ? 'o' : 'r'}="${fi}:${target}"]`)
      }
      el ??= document.getElementById(`file-${fi}`)
      if (!el) return
      el.scrollIntoView({
        block: el.id ? 'start' : 'center',
        behavior: 'smooth',
      })
      el.classList.remove('flash')
      void (el as HTMLElement).offsetWidth
      el.classList.add('flash')
    }, 30)
  }

  return (
    <ReviewCtx.Provider value={reviewApi!}>
      <div class={`app ${chatOpen && data.claude ? 'with-chat' : ''}`}>
        <TopBar
          pendingCount={pendingCount}
          hasPending={!!pendingId}
          view={view}
          setView={(v) => {
            setView(v)
            savePref('view', v)
          }}
          chatOpen={chatOpen}
          toggleChat={() => {
            setChatOpen(!chatOpen)
            savePref('chat', chatOpen ? 'closed' : 'open')
          }}
          onFinish={() => setSubmitOpen(true)}
        />
        {moved && (
          <div class="banner">
            ⟳ New commits were pushed since you loaded this PR.{' '}
            <button class="linkish" onClick={reload} disabled={reloading}>
              {reloading ? 'Reloading…' : 'Reload'}
            </button>{' '}
            (your unsent comment text is kept)
          </div>
        )}
        <div class="body">
          <OrderPanel
            files={files}
            order={order}
            setOrder={setOrder}
            viewedOf={viewedOf}
            viewedCount={viewedCount}
            total={ghFiles.length || files.length}
            currentStep={currentStep}
            jumpTo={jumpTo}
          />
          <main class="mid" ref={mid}>
            {spot && <FileFull spot={spot} trail={trail} />}
            <div hidden={!!spot}>
              <Description />
              {ordered.map(({ file, index }) => (
                <FileView
                  key={file.path}
                  file={file}
                  index={index}
                  viewed={viewedOf(file.path)}
                  collapsed={collapsed.has(file.path)}
                  toggleCollapsed={() => toggleCollapsed(file.path)}
                  setViewed={(v) => setViewed(file.path, v)}
                />
              ))}
              {files.length === 0 && <div class="empty">This PR changes no files.</div>}
            </div>
          </main>
          {chatOpen && data.claude && <ChatPanel ref={chat} />}
        </div>
        {submitOpen && (
          <SubmitDialog
            pendingCount={pendingCount}
            hasPending={!!pendingId}
            onClose={() => setSubmitOpen(false)}
            onDone={(p) => {
              applyThreads(p)
              setSubmitOpen(false)
            }}
            onMoved={() => setMoved(true)}
          />
        )}
        {tip && (
          <CommitTip info={tip.info} rect={tip.rect} keep={() => clearTimeout(tipTimer.current)} leave={() => reviewApi!.hideTip()} />
        )}
        <div class="toasts">
          {toasts.map((t) => (
            <div key={t.id} class="toast">
              {t.msg}
            </div>
          ))}
        </div>
      </div>
    </ReviewCtx.Provider>
  )
}

/** The element that scrolls the middle pane: the pane itself, or the whole body in the narrow layout. */
function scrollerOf(el: HTMLElement | null): HTMLElement | null {
  for (let e = el; e; e = e.parentElement) {
    const overflow = getComputedStyle(e).overflowY
    if (overflow === 'auto' || overflow === 'scroll') return e
  }
  return document.scrollingElement as HTMLElement | null
}

/** Files in reading order: steps first, then groups, then the rest in path order. */
function orderFiles(files: DiffFile[], order: OrderState): { file: DiffFile; index: number }[] {
  const indexed = files.map((file, index) => ({ file, index }))
  const o = order.order
  if (!o) return indexed
  const rank = new Map<number, number>()
  const find = (p: string) => files.findIndex((f) => f.path === p || f.oldPath === p)
  let r = 0
  for (const s of o.steps ?? []) {
    const i = find(s.file)
    if (i >= 0 && !rank.has(i)) rank.set(i, r++)
  }
  for (const g of o.groups ?? [])
    for (const p of g.files) {
      const i = find(p)
      if (i >= 0 && !rank.has(i)) rank.set(i, r++)
    }
  return indexed.sort((a, b) => (rank.get(a.index) ?? 1e9 + a.index) - (rank.get(b.index) ?? 1e9 + b.index))
}

/** The diff line closest at or after `n` on that side, so a step lands on changed code. */
function nearestLine(lines: ReturnType<typeof fileLines>, side: 'new' | 'old', n: number): number | null {
  let best: number | null = null
  for (const l of lines) {
    const v = side === 'old' ? l.old : l.new
    if (!v) continue
    if (v >= n && (best === null || v < best)) best = v
  }
  if (best !== null) return best
  // Step is below every hunk: take the last line.
  for (const l of lines) {
    const v = side === 'old' ? l.old : l.new
    if (v) best = v
  }
  return best
}
