import { render } from 'preact'
import { useState } from 'preact/hooks'
import { postJSON } from '../api'

/** Stops the prview server, after asking once in the page (no browser pop-up). */
export function StopButton() {
  const [sure, setSure] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const stop = async () => {
    try {
      await postJSON('/api/stop', {})
      document.title = 'prview stopped'
      render(<Stopped />, document.getElementById('app')!)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setSure(false)
    }
  }

  if (sure)
    return (
      <span class="confirm stop-confirm">
        Stop the prview server?{' '}
        <button class="linkish danger" onClick={stop}>
          Yes, stop
        </button>{' '}
        <button class="linkish" onClick={() => setSure(false)}>
          No
        </button>
      </span>
    )
  return (
    <button class="btn" onClick={() => setSure(true)} title={error ?? 'Stop the prview server (run prview again to restart it)'}>
      ⏻ Stop
    </button>
  )
}

function Stopped() {
  return (
    <div class="home-page">
      <h1>prview stopped</h1>
      <p class="meta">
        The server is off. Run <code>prview &lt;PR&gt;</code> to start it again, then reload this page.
      </p>
    </div>
  )
}
