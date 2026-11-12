import { nextTick } from 'vue'
import { afterEach, describe, expect, it } from 'vitest'
import { beginUpload, cancelUpload, completeUpload, dismissUpload, failUpload, minimizeUpload, openUpload, prepareUpload, startUpload, updateReading, updateUploadProgress, uploadProgress } from './progress'

afterEach(() => dismissUpload())

describe('upload progress state', () => {
  it('opens expanded and reports reading, transfer, completion and dismissal', async () => {
    beginUpload()
    expect(uploadProgress).toMatchObject({ visible: true, open: true, phase: 'reading', progress: null })

    updateReading(3)
    expect(uploadProgress.label).toContain('3 项')

    prepareUpload('正在检查同名文件…')
    expect(uploadProgress.phase).toBe('reading')
    expect(uploadProgress.label).toBe('正在检查同名文件…')

    startUpload('正在上传 2 个文件到 全部文件')
    updateUploadProgress(512, 1024)
    expect(uploadProgress.phase).toBe('uploading')
    expect(uploadProgress.progress).toBe(50)
    expect(uploadProgress.loaded).toBe(512)
    expect(uploadProgress.total).toBe(1024)

    completeUpload('2 个文件已上传到 全部文件')
    expect(uploadProgress).toMatchObject({ phase: 'complete', progress: 100, loaded: 1024, speed: 0 })

    dismissUpload()
    expect(uploadProgress).toMatchObject({ visible: false, open: false, phase: 'idle', progress: null })
  })

  it('caps in-flight progress below completion and resets the rate on a retry', async () => {
    beginUpload()
    startUpload('正在上传')
    updateUploadProgress(900, 1000)
    expect(uploadProgress.progress).toBe(90)
    updateUploadProgress(1000, 1000)
    expect(uploadProgress.progress).toBe(99)

    startUpload('正在重试上传')
    expect(uploadProgress).toMatchObject({ progress: 0, loaded: 0, total: 0, speed: 0 })
    expect(uploadProgress.phase).toBe('uploading')
  })

  it('minimizes, restores and keeps cancellation or failure details available', async () => {
    beginUpload()
    startUpload('正在上传')
    minimizeUpload()
    expect(uploadProgress.open).toBe(false)
    openUpload()
    expect(uploadProgress.open).toBe(true)

    cancelUpload('已取消上传，原有内容已保留')
    expect(uploadProgress).toMatchObject({ phase: 'cancelled', progress: null })
    expect(uploadProgress.label).toContain('原有内容已保留')

    beginUpload()
    failUpload('网络中断，请重新拖入文件或文件夹重试')
    await nextTick()
    expect(uploadProgress).toMatchObject({ phase: 'error', progress: null })
    expect(uploadProgress.label).toContain('网络中断')
  })
})
