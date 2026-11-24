// File summaries (written by the server in the background) and notes that
// summarize a section or a selection on request. The hook holds their state;
// the pure functions place the Summarize buttons and the notes among the rows.
import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { getJSON, postJSON } from './api'
import type { Row } from './rows'
import type { FileSum, LoadPayload, Note, NoteReq, Section, SummariesPayload } from './types'

export interface SummaryActions {
  sums: SummariesPayload
  /** Asks again for one file's summary. */
  redoSum(path: string): void
  /** The sections of a file side, fetched once per head; see useSections. */
  loadSections(path: string, side: 'new' | 'old'): Promise<Section[]>
  /** Open notes, the most recently opened last. */
  notes: Map<string, Note>
  /** Opens a note and asks for its summary, unless it already has one or is waiting for one. */
  openNote(req: NoteReq): void
  closeNote(key: string): void
}

export function noteKey(r: NoteReq): string {
  return `${r.side}:${r.path}:${r.start}-${r.end}`
}

/** The request that summarizes a section of a file side. */
export function sectionReq(path: string, side: 'new' | 'old', s: Section): NoteReq {
  return { path, side, start: s.start, end: s.end, name: s.name }
}

const NONE: SummariesPayload = { off: '', files: {} }

/** Whether two answers from /summaries say the same, so the page needn't draw again. */
export function sameSums(a: SummariesPayload, b: SummariesPayload): boolean {
  const ka = Object.keys(a.files)
  if (a.off !== b.off || ka.length !== Object.keys(b.files).length) return false
  return ka.every((k) => {
    const x = a.files[k]
    const y = b.files[k]
    return !!y && x.status === y.status && x.text === y.text && x.error === y.error
  })
}

/** Whether a file's sections and lines can be summarized: summaries are on and the file isn't skipped. */
export function summarizable(s: FileSum | undefined): boolean {
  return !!s && s.status !== 'skipped'
}

/** `data` is the latest load: each one fetches the summaries again, and a new head drops sections and notes. */
export function useSummaries(api: string, data: LoadPayload | null, toast: (msg: string) => void): SummaryActions {
  const [sums, setSums] = useState<SummariesPayload>(NONE)
  const [notes, setNotes] = useState(new Map<string, Note>())
  const notesNow = useRef(notes)
  notesNow.current = notes
  // The request each note waits for; an answer to any other is dropped.
  const asking = useRef(new Map<string, number>())
  const nextAsk = useRef(1)
  // Draws again only when something changed: a poll mostly brings the same answer.
  const takeSums = useCallback((p: SummariesPayload) => setSums((cur) => (sameSums(cur, p) ? cur : p)), [])

  useEffect(() => {
    if (!data) return
    let live = true
    getJSON<SummariesPayload>(`${api}/summaries`).then(
      (p) => live && takeSums(p),
      () => {}, // the poll or the next load tries again
    )
    return () => {
      live = false
    }
  }, [api, data, takeSums])

  // Line numbers belong to the head they were read from.
  const head = data?.head
  useEffect(() => {
    asking.current.clear()
    setNotes(new Map())
  }, [api, head])

  // Sections by side and path, for the head on show: a new head makes a new cache.
  const loadSections = useMemo(() => {
    const cache = new Map<string, Promise<Section[]>>()
    return (path: string, side: 'new' | 'old') => {
      const key = `${side}:${path}`
      let got = cache.get(key)
      if (!got) {
        got = getJSON<{ sections: Section[] | null }>(`${api}/sections?${new URLSearchParams({ path, side })}`).then(
          (r) => r.sections ?? [],
          () => [], // no sections: the file just gets no buttons
        )
        cache.set(key, got)
      }
      return got
    }
  }, [api, head]) // not read inside: a new head is what makes the cache stale

  const busy = Object.values(sums.files).some((f) => f.status === 'waiting' || f.status === 'running')
  useEffect(() => {
    if (!busy) return
    const t = setInterval(async () => {
      try {
        takeSums(await getJSON<SummariesPayload>(`${api}/summaries`))
      } catch {
        // try again next tick
      }
    }, 3000)
    return () => clearInterval(t)
  }, [busy, api, takeSums])

  const redoSum = useCallback(
    async (path: string) => {
      try {
        takeSums(await postJSON<SummariesPayload>(`${api}/summaries/redo`, { path }))
      } catch (e) {
        toast(e instanceof Error ? e.message : String(e))
      }
    },
    [api, toast, takeSums],
  )

  const openNote = useCallback(
    (req: NoteReq) => {
      const key = noteKey(req)
      const held = notesNow.current.get(key)
      const ask = !held || held.status === 'failed'
      // Put it last, as the most recently opened.
      setNotes((m) => {
        const n = new Map(m)
        n.delete(key)
        n.set(key, ask ? { req, status: 'running', text: '', error: '' } : held!)
        return n
      })
      if (!ask) return
      const id = nextAsk.current++
      asking.current.set(key, id)
      postJSON<{ summary: string }>(`${api}/summary/section`, req)
        .then(
          (r): Partial<Note> => ({ status: 'ready', text: r.summary }),
          (e): Partial<Note> => ({ status: 'failed', error: e instanceof Error ? e.message : String(e) }),
        )
        .then((p) => {
          if (asking.current.get(key) !== id) return
          asking.current.delete(key)
          setNotes((m) => {
            const cur = m.get(key)
            if (!cur) return m
            return new Map(m).set(key, { ...cur, ...p })
          })
        })
    },
    [api],
  )

  const closeNote = useCallback((key: string) => {
    asking.current.delete(key)
    setNotes((m) => {
      if (!m.has(key)) return m
      const n = new Map(m)
      n.delete(key)
      return n
    })
  }, [])

  return { sums, redoSum, loadSections, notes, openNote, closeNote }
}

/**
 * The sections of a file side, or null while they load or when `want` is
 * false. Only the component that asks draws again when they arrive, so the
 * sections of many files coming in one by one don't redraw the whole page.
 */
export function useSections(load: SummaryActions['loadSections'], path: string, side: 'new' | 'old', want: boolean): Section[] | null {
  const [got, setGot] = useState<{ load: typeof load; path: string; side: string; sections: Section[] } | null>(null)
  useEffect(() => {
    if (!want) return
    let live = true
    load(path, side).then((sections) => live && setGot({ load, path, side, sections }))
    return () => {
      live = false
    }
  }, [load, path, side, want])
  return want && got && got.load === load && got.path === path && got.side === side ? got.sections : null
}

/** The line number a row shows on a side, if it shows one. */
function lineOn(r: Row, side: 'new' | 'old'): number | undefined {
  if (r.t !== 'line') return undefined
  return side === 'old' ? r.line.old : r.line.new
}

/**
 * Which row gets each section's Summarize button: the row of the section's
 * first line when it is shown; when that line is hidden, the gap row just
 * above the section's first shown line (the hunk's @@ line); else that first
 * shown line. Sections with no shown rows get no button.
 */
export function sectionButtons(rows: Row[], sections: Section[], side: 'new' | 'old'): Map<number, Section> {
  const out = new Map<number, Section>()
  for (const s of sections) {
    const start = rows.findIndex((r) => lineOn(r, side) === s.start)
    if (start >= 0) {
      out.set(start, s)
      continue
    }
    const first = rows.findIndex((r) => {
      const n = lineOn(r, side)
      return n !== undefined && n >= s.start && n <= s.end
    })
    if (first < 0) continue
    // Step over the hunk's lines that have no number on this side (removed lines, for the new side).
    let above = first - 1
    while (above >= 0 && rows[above].t === 'line' && lineOn(rows[above], side) === undefined) above--
    out.set(above >= 0 && rows[above].t === 'gap' ? above : first, s)
  }
  return out
}

/**
 * Which row each note hangs under: a section's note under its button, any
 * other under the last shown row of its lines. `buttonSide` is the side the
 * buttons were placed for.
 */
export function noteRows(rows: Row[], buttons: Map<number, Section>, buttonSide: 'new' | 'old', notes: Note[]): Map<number, Note[]> {
  const out = new Map<number, Note[]>()
  for (const note of notes) {
    const { side, start, end } = note.req
    let at = -1
    if (side === buttonSide)
      for (const [i, s] of buttons)
        if (s.start === start && s.end === end) {
          at = i
          break
        }
    if (at < 0)
      for (let i = rows.length - 1; i >= 0; i--) {
        const n = lineOn(rows[i], side)
        if (n !== undefined && n >= start && n <= end) {
          at = i
          break
        }
      }
    if (at >= 0) out.set(at, [...(out.get(at) ?? []), note])
  }
  return out
}
