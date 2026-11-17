import { render } from 'preact'
import { Home } from './Home'
import { Review } from './Review'
import './styles.css'

const m = window.location.pathname.match(/^\/([^/]+)\/([^/]+)\/pull\/(\d+)\/?$/)

render(
  m ? <Review owner={decodeURIComponent(m[1])} repo={decodeURIComponent(m[2])} number={Number(m[3])} /> : <Home />,
  document.getElementById('app')!,
)
