import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchFile } from './lines'

const getJSON = vi.hoisted(() => vi.fn())
vi.mock('./api', () => ({ getJSON }))

describe('fetchFile', () => {
  beforeEach(() => {
    getJSON.mockReset()
    getJSON.mockImplementation(async () => ({ lines: [`call ${getJSON.mock.calls.length}`] }))
  })

  it('fetches a file once per commit', async () => {
    const api = '/api/pr/o/r/1'
    expect(await fetchFile(api, 'new', 'head1', 'a.go')).toEqual(['call 1'])
    expect(await fetchFile(api, 'new', 'head1', 'a.go')).toEqual(['call 1'])
    // A reload brought a new head: the old text is not shown for it.
    expect(await fetchFile(api, 'new', 'head2', 'a.go')).toEqual(['call 2'])
    expect(getJSON).toHaveBeenCalledTimes(2)
    expect(getJSON).toHaveBeenLastCalledWith(`${api}/lines?side=new&path=a.go`)
  })

  it('tries again after a failed fetch', async () => {
    getJSON.mockRejectedValueOnce(new Error('offline'))
    await expect(fetchFile('/x', 'old', 'base1', 'b.go')).rejects.toThrow('offline')
    expect(await fetchFile('/x', 'old', 'base1', 'b.go')).toEqual(['call 2'])
  })
})
