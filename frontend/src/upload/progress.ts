import { reactive } from 'vue'

export type UploadPhase = 'idle' | 'reading' | 'uploading' | 'complete' | 'error' | 'cancelled'

export interface UploadProgressState {
  /** Whether an upload task exists and can be restored from the header. */
  visible: boolean
  /** Whether the progress dialog is expanded. A minimized task keeps `visible` true. */
  open: boolean
  phase: UploadPhase
  label: string
  progress: number | null
  loaded: number
  total: number
  /** Smoothed transfer rate in bytes per second. */
  speed: number
}

const initial = (): UploadProgressState => ({ visible: false, open: false, phase: 'idle', label: '', progress: null, loaded: 0, total: 0, speed: 0 })

export const uploadProgress = reactive<UploadProgressState>(initial())

let lastAt = 0
let lastLoaded = 0

function resetTransferRate() {
  lastAt = 0
  lastLoaded = 0
}

export function beginUpload() {
  Object.assign(uploadProgress, initial(), { visible: true, open: true, phase: 'reading', label: '正在读取文件和文件夹…' })
  resetTransferRate()
}

export function updateReading(count: number) {
  if (!uploadProgress.visible || uploadProgress.phase !== 'reading') return
  uploadProgress.label = `正在读取文件和文件夹… ${count} 项`
}

export function prepareUpload(label: string) {
  if (!uploadProgress.visible) return
  uploadProgress.phase = 'reading'
  uploadProgress.label = label
  uploadProgress.progress = null
  uploadProgress.speed = 0
  resetTransferRate()
}

export function startUpload(label: string) {
  uploadProgress.phase = 'uploading'
  uploadProgress.label = label
  uploadProgress.progress = 0
  uploadProgress.loaded = 0
  uploadProgress.total = 0
  uploadProgress.speed = 0
  resetTransferRate()
}

export function updateUploadProgress(loaded: number, total: number) {
  if (!uploadProgress.visible || uploadProgress.phase !== 'uploading') return
  const now = Date.now()
  if (lastAt && now > lastAt && loaded >= lastLoaded) {
    const sample = (loaded - lastLoaded) / ((now - lastAt) / 1000)
    uploadProgress.speed = uploadProgress.speed ? uploadProgress.speed * 0.7 + sample * 0.3 : sample
  }
  if (loaded < lastLoaded) uploadProgress.speed = 0
  lastAt = now
  lastLoaded = loaded
  uploadProgress.loaded = loaded
  uploadProgress.total = total
  if (total > 0) uploadProgress.progress = Math.min(99, Math.round(loaded / total * 100))
}

export function completeUpload(label: string) {
  uploadProgress.phase = 'complete'
  uploadProgress.label = label
  uploadProgress.progress = 100
  uploadProgress.speed = 0
  if (uploadProgress.total > 0) uploadProgress.loaded = uploadProgress.total
  resetTransferRate()
}

export function cancelUpload(label: string) {
  uploadProgress.phase = 'cancelled'
  uploadProgress.label = label
  uploadProgress.progress = null
  uploadProgress.speed = 0
  resetTransferRate()
}

export function failUpload(label: string) {
  uploadProgress.phase = 'error'
  uploadProgress.label = label
  uploadProgress.progress = null
  uploadProgress.speed = 0
  resetTransferRate()
}

export function minimizeUpload() {
  if (uploadProgress.visible) uploadProgress.open = false
}

export function openUpload() {
  if (uploadProgress.visible) uploadProgress.open = true
}

export function dismissUpload() {
  Object.assign(uploadProgress, initial())
  resetTransferRate()
}
