import { describe, expect, it } from 'vitest'
import { createSVG, matrix } from './qrcode'

describe('QR code renderer', () => {
  it('builds a bounded QR matrix and an accessible SVG asset', () => {
    const modules = matrix('http://192.168.1.5:8080')
    expect(modules.length).toBeGreaterThanOrEqual(21)
    expect(modules.length).toBeLessThanOrEqual(37)
    expect(modules.every(row => row.length === modules.length && row.every(cell => typeof cell === 'boolean'))).toBe(true)
    const svg = createSVG('http://192.168.1.5:8080')
    expect(svg.namespaceURI).toBe('http://www.w3.org/2000/svg')
    expect(svg.getAttribute('viewBox')).toMatch(/^0 0 \d+ \d+$/)
    expect(svg.querySelector('path')?.getAttribute('d')).toContain('M')
  })

  it('rejects addresses that exceed the supported QR versions', () => {
    expect(() => matrix('x'.repeat(200))).toThrow('二维码地址过长')
  })
})
