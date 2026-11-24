import { describe, expect, it } from 'vitest'
import { clickTarget, groupPlaces, hashToSpot, spotToHash } from './nav'
import type { Place } from './types'

describe('clickTarget', () => {
  const go = 'if err := shareddb.ApplyLoginFromEnv(db, "PIPELINE_APP_USER") // why'
  it('finds the name and its qualifier', () => {
    expect(clickTarget(go, 'go', go.indexOf('Login'))).toEqual({ name: 'ApplyLoginFromEnv', hint: 'shareddb', col: go.indexOf('Apply') + 1 })
  })
  it('ignores keywords, strings, comments and punctuation', () => {
    expect(clickTarget(go, 'go', 0)).toBeNull() // "if"
    expect(clickTarget(go, 'go', go.indexOf('PIPELINE'))).toBeNull()
    expect(clickTarget(go, 'go', go.indexOf('why'))).toBeNull()
    expect(clickTarget(go, 'go', go.indexOf('('))).toBeNull()
  })
  it('takes the C++ qualifier', () => {
    const cpp = 'Chain CameraChain::Build(const Cfg& c)'
    expect(clickTarget(cpp, 'cpp', cpp.indexOf('Build') + 2)).toEqual({ name: 'Build', hint: 'CameraChain', col: cpp.indexOf('Build') + 1 })
    expect(clickTarget('node->Process(f);', 'cpp', 8)?.hint).toBe('node')
  })
  it('works without a known language', () => {
    expect(clickTarget('run_all now', null, 2)).toEqual({ name: 'run_all', hint: '', col: 1 })
  })
})

describe('spot hash', () => {
  it('round-trips, with the old side marked', () => {
    const s = { path: 'services/shared/database/applogin.go', line: 88, side: 'new' as const }
    expect(spotToHash(s)).toBe('#file=services/shared/database/applogin.go&line=88')
    expect(hashToSpot(spotToHash(s))).toEqual(s)
    expect(hashToSpot('#file=a%20b.go&line=3&side=old')).toEqual({ path: 'a b.go', line: 3, side: 'old' })
    expect(hashToSpot('#nothing')).toBeNull()
  })
})

describe('groupPlaces', () => {
  const place = (path: string, kind: Place['kind']): Place => ({ path, line: 1, col: 1, text: '', kind, changed: false })
  it('splits by kind and keeps order', () => {
    const ps = [place('a', 'use'), place('b', 'definition'), place('c', 'comment'), place('d', 'use'), place('e', 'definition')]
    const g = groupPlaces(ps)
    expect(g.definition.map((p) => p.path)).toEqual(['b', 'e'])
    expect(g.use.map((p) => p.path)).toEqual(['a', 'd'])
    expect(g.comment.map((p) => p.path)).toEqual(['c'])
  })
})
