import { useState } from 'preact/hooks'
import { useReview } from '../ctx'
import { safeHTML, timeAgo } from '../util'

/** The PR description and the general conversation, read only. */
export function Description() {
  const { data } = useReview()
  const pr = data.pr
  const comments = pr.comments ?? []
  const [open, setOpen] = useState(false)
  return (
    <section class="desc">
      <button class="desc-head" onClick={() => setOpen(!open)}>
        {open ? '▾' : '▸'} Description
        {comments.length > 0 && ` and ${comments.length} conversation comment${comments.length === 1 ? '' : 's'}`}
      </button>
      {open && (
        <div class="desc-body">
          <div class="cm">
            <div class="who">
              {pr.author} <span>opened this PR</span>
            </div>
            {pr.bodyHTML ? <div class="md" dangerouslySetInnerHTML={{ __html: safeHTML(pr.bodyHTML) }} /> : <div class="meta">No description.</div>}
          </div>
          {comments.map((c) => (
            <div class="cm" key={c.id}>
              <div class="who">
                {c.author} <span>· {timeAgo(c.createdAt)}</span>
              </div>
              <div class="md" dangerouslySetInnerHTML={{ __html: safeHTML(c.bodyHTML) }} />
            </div>
          ))}
          <div class="meta desc-note">To add a general comment, write it in the summary when you finish your review.</div>
        </div>
      )}
    </section>
  )
}
