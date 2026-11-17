import DOMPurify from 'dompurify'
import { marked } from 'marked'
import type { Commit } from './types'

/** Markdown (from Claude) to safe HTML. */
export function markdown(text: string): string {
  return DOMPurify.sanitize(marked.parse(text, { async: false, gfm: true, breaks: false }) as string)
}

/** GitHub's own rendered HTML, cleaned once more before it goes in the page. */
export function safeHTML(html: string): string {
  return DOMPurify.sanitize(html)
}

export function timeAgo(iso: string): string {
  const s = (Date.now() - new Date(iso).getTime()) / 1000
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  if (s < 86400 * 30) return `${Math.floor(s / 86400)} days ago`
  return new Date(iso).toLocaleDateString()
}

/** Per-commit colours; picked to read on light and dark backgrounds. */
const palette = ['#8250df', '#0969da', '#bf3989', '#1a7f37', '#bc4c00', '#0a7d8c', '#9a6700', '#cf222e', '#6639ba', '#3b6ea8']

export interface CommitInfo {
  commit: Commit
  index: number // 1-based
  total: number
  color: string
}

export function commitIndex(commits: Commit[]): Map<string, CommitInfo> {
  const m = new Map<string, CommitInfo>()
  commits.forEach((c, i) => m.set(c.oid, { commit: c, index: i + 1, total: commits.length, color: palette[i % palette.length] }))
  return m
}

export function loadPref<T extends string>(key: string, fallback: T): T {
  try {
    return (localStorage.getItem('prview.' + key) as T) || fallback
  } catch {
    return fallback
  }
}

export function savePref(key: string, value: string) {
  try {
    localStorage.setItem('prview.' + key, value)
  } catch {
    // private window or blocked storage; the choice just isn't remembered
  }
}

export function plural(n: number, word: string) {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}
