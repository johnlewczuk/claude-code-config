// File summaries (written by the server in the background) and notes that
// summarize a section or a selection on request. The hook holds their state;
// the pure functions place the Summarize buttons and the notes among the rows.
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { getJSON, postJSON } from './api'
import type { Row } from './rows'
import type { LoadPayload, Note, NoteReq, Section, SummariesPayload } from './types'

export interface SummaryActions {
  sums: SummariesPayload
  /** Asks again for one file's summary. */
  redoSum(path: string): void
  /** The sections of a file side, fetched once and then kept; null while loading. */
  sectionsOf(path: string, side: 'new' | 'old'): Section[] | null
  /** Open notes, the most recently opened last. */
  notes: Map<string, Note>
  /** Opens a note and asks for its summary, unless it already has one or is waiting for one. */
  openNote(req: NoteReq): void
  closeNote(key: string): void
}

export function noteKey(r: NoteReq): string {
  return `${r.side}:${r.path}:${r.start}-${r.end}`
}

const NONE: SummariesPayload = { off: '', files: {} }

/** `data` is the latest load: each one fetches the summaries again, and a new head drops sections and notes. */
export function useSummaries(api: string, data: LoadPayload | null, toast: (msg: string) => void): SummaryActions {
  const [sums, setSums] = useState<SummariesPayload>(NONE)
  const [notes, setNotes] = useState(new Map<string, Note>())
  const notesNow = useRef(notes)
  notesNow.current = notes
  // The request each note waits for; an answer to any other is dropped.
  const asking = useRef(new Map<string, number>())
  const nextAsk = useRef(1)
  // Sections by side and path, for the head on show. Replaced, not cleared, on a new head,
  // so a fetch for the old head fills a cache no one reads.
  const sections = useRef(new Map<string, Section[] | null>())
  const [, bump] = useState(0)

  useEffect(() => {
    if (!data) return
    let live = true
    getJSON<SummariesPayload>(`${api}/summaries`).then(
      (p) => live && setSums(p),
      () => {}, // the poll or the next load tries again
    )
    return () => {
      live = false
    }
  }, [api, data])

  // Line numbers belong to the head they were read from.
  const head = data?.head
  useEffect(() => {
    sections.current = new Map()
    asking.current.clear()
    setNotes(new Map())
  }, [api, head])

  const busy = Object.values(sums.files).some((f) => f.status === 'waiting' || f.status === 'running')
  useEffect(() => {
    if (!busy) return
    const t = setInterval(async () => {
      try {
        setSums(await getJSON<SummariesPayload>(`${api}/summaries`))
      } catch {
        // try again next tick
      }
    }, 3000)
    return () => clearInterval(t)
  }, [busy, api])

  const redoSum = useCallback(
    async (path: string) => {
      try {
        setSums(await postJSON<SummariesPayload>(`${api}/summaries/redo`, { path }))
      } catch (e) {
        toast(e instanceof Error ? e.message : String(e))
      }
    },
    [api, toast],
  )

  const sectionsOf = useCallback(
    (path: string, side: 'new' | 'old') => {
      const cache = sections.current
      const key = `${side}:${path}`
      if (cache.has(key)) return cache.get(key)!
      cache.set(key, null)
      getJSON<{ sections: Section[] | null }>(`${api}/sections?${new URLSearchParams({ path, side })}`)
        .then(
          (r) => r.sections ?? [],
          () => [], // no sections: the file just gets no buttons
        )
        .then((s) => {
          cache.set(key, s)
          if (cache === sections.current) bump((n) => n + 1)
        })
      return null
    },
    [api],
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

  return { sums, redoSum, sectionsOf, notes, openNote, closeNote }
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
