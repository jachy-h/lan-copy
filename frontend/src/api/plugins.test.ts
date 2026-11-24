import { describe, expect, it } from 'vitest'
import { parsePluginDialogs } from './plugins'

describe('parsePluginDialogs', () => {
  it('keeps same-origin plugin proxy pages and defaults the title', () => {
    expect(parsePluginDialogs([
      { pluginId: 'invoice-ocr', pluginName: 'Invoice OCR', title: '', url: '/plugin-app/invoice-ocr/review/abc' },
    ])).toEqual([
      { pluginId: 'invoice-ocr', pluginName: 'Invoice OCR', title: 'Invoice OCR', url: '/plugin-app/invoice-ocr/review/abc' },
    ])
  })

  it('drops external, protocol-relative and malformed entries', () => {
    expect(parsePluginDialogs([
      { pluginId: 'a', url: 'https://example.com/x' },
      { pluginId: 'b', url: '//example.com/x' },
      { pluginId: 'c', url: '/plugin-app/c/../secret' },
      { pluginId: '', url: '/plugin-app/d/x' },
      { pluginId: 'e', url: '/other/x' },
      null,
      'x',
    ])).toEqual([])
  })

  it('returns an empty list for non-array values', () => {
    expect(parsePluginDialogs(undefined)).toEqual([])
    expect(parsePluginDialogs({ pluginId: 'a' })).toEqual([])
  })
})
