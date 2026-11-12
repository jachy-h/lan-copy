import { describe, expect, it } from 'vitest'
import { previewFailure } from './errors'

describe('preview failures', () => {
  it('retains stage, error name and missing API instead of hiding the cause', () => {
    const result = previewFailure('加载 PDF 组件', new TypeError('Promise.withResolvers is not a function'))
    expect(result.summary).toContain('加载 PDF 组件')
    expect(result.summary).toContain('浏览器缺少')
    expect(result.detail).toBe('TypeError: Promise.withResolvers is not a function')
  })
  it('distinguishes password, invalid PDF, worker and network failures', () => {
    const error = new Error('password'); error.name = 'PasswordException'
    expect(previewFailure('解析 PDF', error).summary).toContain('密码')
    error.name = 'InvalidPDFException'
    expect(previewFailure('解析 PDF', error).summary).toContain('不完整')
    expect(previewFailure('解析 PDF', new Error('Setting up fake worker failed')).summary).toContain('后台解析')
    expect(previewFailure('请求文件', new Error('HTTP 503')).detail).toContain('HTTP 503')
  })
})
