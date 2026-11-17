import { useState } from 'preact/hooks'
import { StopButton } from './components/StopButton'

const linkRe = /github\.com\/([^/\s]+)\/([^/\s]+)\/pull\/(\d+)/
const shortRe = /^([^/\s]+)\/([^#\s]+)#(\d+)$/

/** Paste a PR link to open it. For machines where the command isn't at hand. */
export function Home() {
  const [value, setValue] = useState('')
  const [error, setError] = useState('')
  const go = (e: Event) => {
    e.preventDefault()
    const m = value.trim().match(linkRe) ?? value.trim().match(shortRe)
    if (!m) {
      setError('Paste a PR link like https://github.com/owner/repo/pull/123, or owner/repo#123.')
      return
    }
    window.location.href = `/${m[1]}/${m[2]}/pull/${m[3]}`
  }
  return (
    <div class="home-page">
      <h1>prview</h1>
      <p class="meta">Review a GitHub pull request: commits on every line, a reading order, and Claude beside the diff.</p>
      <form onSubmit={go} class="home-form">
        <input
          class="mock-input"
          value={value}
          onInput={(e) => setValue((e.target as HTMLInputElement).value)}
          placeholder="https://github.com/owner/repo/pull/123"
          autoFocus
        />
        <button class="btn green" type="submit">
          Open
        </button>
      </form>
      {error && <div class="error-box">{error}</div>}
      <div class="home-foot">
        <StopButton />
      </div>
    </div>
  )
}
