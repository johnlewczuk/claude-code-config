// Calls to the Go server. Every PR call is under /api/pr/{owner}/{repo}/{n}.

export class ApiError extends Error {
  constructor(message: string, public status: number) {
    super(message)
  }
}

export function prBase(owner: string, repo: string, n: number) {
  return `/api/pr/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/${n}`
}

export async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url, { credentials: 'same-origin' })
  return readResponse<T>(r)
}

export async function postJSON<T>(url: string, body: unknown): Promise<T> {
  const r = await fetch(url, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body ?? {}),
  })
  return readResponse<T>(r)
}

async function readResponse<T>(r: Response): Promise<T> {
  const text = await r.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    // not JSON
  }
  if (!r.ok) {
    throw new ApiError(data?.error ?? (text || r.statusText), r.status)
  }
  return data as T
}

/**
 * Reads a server-sent event stream from a GET or POST and calls onEvent for
 * each event. Resolves when the stream ends.
 */
export async function stream(
  url: string,
  body: unknown | undefined,
  onEvent: (event: string, data: any) => void,
  signal?: AbortSignal,
): Promise<void> {
  const init: RequestInit = { credentials: 'same-origin', signal }
  if (body !== undefined) {
    init.method = 'POST'
    init.headers = { 'Content-Type': 'application/json' }
    init.body = JSON.stringify(body)
  }
  const r = await fetch(url, init)
  if (!r.ok || !r.body) {
    await readResponse(r)
    return
  }
  const reader = r.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    let i: number
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const chunk = buf.slice(0, i)
      buf = buf.slice(i + 2)
      let event = 'message'
      let data = ''
      for (const line of chunk.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7)
        else if (line.startsWith('data: ')) data += line.slice(6)
      }
      try {
        onEvent(event, data ? JSON.parse(data) : null)
      } catch (e) {
        console.error('bad event', event, e)
      }
    }
  }
}
