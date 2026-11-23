// Whole files fetched for the peek and the whole-file view, kept so stepping
// back to one is instant.
import { getJSON } from './api'

const files = new Map<string, Promise<string[]>>()

/**
 * The lines of path on one side of the PR. rev is the commit that side is
 * at (the loaded head, or the base), so a reload that brings a new head
 * fetches the new text instead of showing the old.
 */
export function fetchFile(api: string, side: 'new' | 'old', rev: string, path: string): Promise<string[]> {
  const key = `${api}|${rev}|${path}`
  let p = files.get(key)
  if (!p) {
    if (files.size >= 40) files.delete(files.keys().next().value!)
    p = getJSON<{ lines: string[] }>(`${api}/lines?side=${side}&path=${encodeURIComponent(path)}`).then((r) => r.lines ?? [])
    p.catch(() => files.delete(key))
    files.set(key, p)
  }
  return p
}
