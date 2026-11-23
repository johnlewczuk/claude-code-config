// State of the peek window: where it hangs, and the levels of lookups the
// reviewer has drilled through. Each level asks the server (/refs) for its name.
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { stream } from './api'
import type { RefsResult } from './types'

/** Where a peek hangs: under a diff line, or under a line of the whole-file view. */
export type PeekAnchor = { fileIndex: number; idx: number; line?: number } | { full: true; line: number }

/** A name looked up, and where it was clicked. */
export interface PeekTarget {
  name: string
  hint: string
  col: number
  path: string
  side: 'new' | 'old'
  line: number
}

export interface PeekLevel {
  id: number
  target: PeekTarget
  status: 'loading' | 'ready' | 'failed'
  progress?: string
  error?: string
  result?: RefsResult
  /** Index into result.places of the place shown. */
  chosen: number
  /** Where the shown code was scrolled to. */
  scroll?: number
}

export interface PeekState {
  anchor: PeekAnchor
  levels: PeekLevel[]
}

export interface PeekActions {
  peek: PeekState | null
  openPeek(at: PeekAnchor, target: PeekTarget): void
  closePeek(): void
  /** Look up another name from the code shown inside the peek. */
  drill(t: { name: string; hint: string; col: number; line: number }): void
  peekBack(): void
  choosePlace(i: number): void
  /** Remembers the code's scroll for the level; changes no display. */
  saveScroll(levelId: number, top: number): void
}

export function usePeek(api: string): PeekActions {
  const [peek, setPeek] = useState<PeekState | null>(null)
  const now = useRef(peek)
  now.current = peek
  const nextId = useRef(1)
  const running = useRef(new Map<number, AbortController>())

  const stop = (id: number) => {
    running.current.get(id)?.abort()
    running.current.delete(id)
  }
  const stopAll = () => [...running.current.keys()].forEach(stop)
  useEffect(() => stopAll, [])
  useEffect(stopAll, [api])

  const patch = (id: number, p: Partial<PeekLevel>) =>
    setPeek(
      (s) =>
        s && {
          ...s,
          levels: s.levels.map((l) => (l.id === id ? { ...l, ...p } : l)),
        },
    )

  const start = (target: PeekTarget): PeekLevel => {
    const id = nextId.current++
    const ctl = new AbortController()
    running.current.set(id, ctl)
    const q = new URLSearchParams({
      path: target.path,
      side: target.side,
      line: String(target.line),
      col: String(target.col),
      name: target.name,
      hint: target.hint,
    })
    ;(async () => {
      let failed: string | null = null
      let done = false
      try {
        await stream(
          `${api}/refs?${q}`,
          undefined,
          (event, d) => {
            if (event === 'progress') patch(id, { progress: d.message })
            else if (event === 'failed') failed = d.error
            else if (event === 'ready') {
              const result = d as RefsResult
              const places = result.places ?? []
              const first = places.findIndex((p) => p.kind === 'definition')
              done = true
              patch(id, {
                status: 'ready',
                result: { ...result, places },
                chosen: first >= 0 ? first : 0,
              })
            }
          },
          ctl.signal,
        )
      } catch (e) {
        if (ctl.signal.aborted) return
        failed = e instanceof Error ? e.message : String(e)
      }
      running.current.delete(id)
      if (!done)
        patch(id, {
          status: 'failed',
          error: failed ?? 'The server gave no answer.',
        })
    })()
    return { id, target, status: 'loading', chosen: 0 }
  }

  const openPeek = useCallback(
    (anchor: PeekAnchor, target: PeekTarget) => {
      stopAll()
      setPeek({ anchor, levels: [start(target)] })
    },
    [api],
  )

  const closePeek = useCallback(() => {
    stopAll()
    setPeek(null)
  }, [])

  const drill = useCallback(
    (t: { name: string; hint: string; col: number; line: number }) => {
      const s = now.current
      const top = s?.levels[s.levels.length - 1]
      const place = top?.result?.places[top.chosen]
      if (!s || !top || !place) return
      const level = start({ ...t, path: place.path, side: top.target.side })
      setPeek({ ...s, levels: [...s.levels, level] })
    },
    [api],
  )

  const peekBack = useCallback(() => {
    const s = now.current
    if (!s || s.levels.length < 2) return
    stop(s.levels[s.levels.length - 1].id)
    setPeek({ ...s, levels: s.levels.slice(0, -1) })
  }, [])

  const choosePlace = useCallback((i: number) => {
    setPeek((s) => {
      if (!s) return s
      const levels = s.levels.slice()
      const top = levels.length - 1
      if (levels[top].chosen === i) return s
      levels[top] = { ...levels[top], chosen: i, scroll: undefined }
      return { ...s, levels }
    })
  }, [])

  const saveScroll = useCallback((levelId: number, top: number) => {
    const l = now.current?.levels.find((x) => x.id === levelId)
    if (l) l.scroll = top
  }, [])

  return {
    peek,
    openPeek,
    closePeek,
    drill,
    peekBack,
    choosePlace,
    saveScroll,
  }
}
