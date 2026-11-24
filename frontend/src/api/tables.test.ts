import { afterEach, describe, expect, it, vi } from 'vitest'
import { saveTableLayout } from './tables'

afterEach(() => vi.unstubAllGlobals())

describe('saveTableLayout', () => {
  it('sends the layout with the table write action header', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      path: 'data/orders.csv',
      layout: { version: 1, order: ['status', 'id'], hidden: ['note'] },
    }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    const input = { path: 'data/orders.csv', order: ['status', 'id'], hidden: ['note'] }
    await expect(saveTableLayout(input)).resolves.toEqual({
      path: 'data/orders.csv',
      layout: { version: 1, order: ['status', 'id'], hidden: ['note'] },
    })
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect((init.headers as Headers).get('X-Lan-Copy-Action')).toBe('tables')
    expect(JSON.parse(String(init.body))).toEqual({ version: 1, ...input })
  })
})
