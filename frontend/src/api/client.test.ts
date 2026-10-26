import { afterEach, describe, expect, it, vi } from 'vitest'
import { requestJSON } from './client'

afterEach(() => vi.unstubAllGlobals())

describe('requestJSON', () => {
  it('preserves caller headers and parses JSON', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"ok":true}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(requestJSON<{ ok: boolean }>('/api/app', { headers: { 'X-Test': 'yes' } })).resolves.toEqual({ ok: true })
    expect(fetchMock.mock.calls[0][1].headers.get('Accept')).toBe('application/json')
    expect(fetchMock.mock.calls[0][1].headers.get('X-Test')).toBe('yes')
  })

  it('preserves structured conflict fields for confirmation flows', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"error":"冲突","requiresConfirmation":true}', { status: 409 })))
    await expect(requestJSON('/api/files/move', { method: 'POST' })).rejects.toMatchObject({
      status: 409,
      message: '冲突',
      body: { requiresConfirmation: true },
    })
  })
})
