import { describe, expect, it } from 'vitest'
import { buildAgentPrompt } from './pluginPrompt'

describe('buildAgentPrompt', () => {
  it('contains protocol, manifest, security and API guidance', () => {
    const prompt = buildAgentPrompt('http://127.0.0.1:18080')
    for (const expected of [
      'http://127.0.0.1:18080',
      'GET /health',
      'GET /manifest',
      'GET /version',
      'GET /capabilities',
      'io.lan-copy.plugin.manifest',
      'protocolVersion',
      'USER 65532:65532',
      '非 root',
      'Docker Socket',
      'plugins-image',
      'X-Lan-Copy-Action: plugins',
      '/api/plugins/install',
      '原地升级',
      'LAN_COPY_EVENT_TOKEN',
      'LAN_COPY_API_TOKEN',
      'lock-and-update',
      'POST /events',
      'folder.*',
      '插件 Web 界面',
      'X-Forwarded-Prefix',
      '/plugin-app/<plugin-id>/',
      '/api/plugins/<plugin-id>/events/test',
      '上传 Hook',
      'uploadHook',
      'X-Lan-Copy-Hook',
      'lan-copy:plugin-dialog:complete',
      'frame-ancestors',
      '设置 → 插件',
    ]) {
      expect(prompt).toContain(expected)
    }
  })

  it('includes the manifest and upload lifecycle', () => {
    const prompt = buildAgentPrompt('http://localhost:8080')
    expect(prompt).toContain('"schemaVersion": 1')
    expect(prompt).toContain('/api/plugins/images/upload')
    expect(prompt).toContain('/api/plugins/tasks/')
    expect(prompt).toContain('默认保留镜像和数据')
  })
})
