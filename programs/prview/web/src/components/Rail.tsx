type Side = 'left' | 'right'

/** What a closed side panel leaves behind: a thin strip that opens it again. */
export function Rail({ side, label, onOpen }: { side: Side; label: string; onOpen(): void }) {
  return (
    <button class={`rail ${side}`} onClick={onOpen} title={`Show ${label}`} aria-label={`Show ${label}`}>
      <span class="rail-arrow">{side === 'left' ? '»' : '«'}</span>
      <span class="rail-label">{label}</span>
    </button>
  )
}

/** The button in a side panel's heading that closes it to a rail. */
export function HideButton({ side, label, onHide }: { side: Side; label: string; onHide(): void }) {
  return (
    <button class="linkish side-hide" onClick={onHide} title={`Hide ${label}`} aria-label={`Hide ${label}`}>
      {side === 'left' ? '«' : '»'}
    </button>
  )
}
