export interface UploadEntry { path: string; directory: boolean; file?: File }
const LIMIT = 10_000

function collector(progress: (count: number) => void = () => {}) {
  const entries: UploadEntry[] = []
  function add(entry: UploadEntry) {
    if (entry.path.split('/').length > 64) throw new Error('文件夹层级过深，最多支持 64 层')
    if (entries.length >= LIMIT) throw new Error('文件和目录合计超过 10000 项，请分批上传')
    entries.push(entry)
    if (entries.length === 1 || entries.length % 50 === 0) progress(entries.length)
  }
  return { entries, add }
}

export async function fromEntries(roots: FileSystemEntry[], progress: (count: number) => void = () => {}): Promise<UploadEntry[]> {
  const { entries, add } = collector(progress)
  async function visit(entry: FileSystemEntry, prefix = ''): Promise<void> {
    const relativePath = prefix + entry.name
    if (entry.isDirectory) {
      add({ path: relativePath, directory: true })
      const directory = entry as FileSystemDirectoryEntry
      const reader = directory.createReader()
      // Chromium yields directory children in batches; read until the empty batch.
      for (;;) {
        const children = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject))
        if (!children.length) break
        for (const child of children) await visit(child, `${relativePath}/`)
      }
    } else if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject))
      add({ path: relativePath, directory: false, file })
    } else throw new Error(`无法读取“${relativePath}”，请检查文件权限`)
  }
  for (const root of roots) await visit(root)
  return entries
}

export function fromFiles(files: File[], requireRelative = false, progress?: (count: number) => void): UploadEntry[] {
  const { entries, add } = collector(progress)
  const directories = new Set<string>()
  for (const file of files) {
    if (requireRelative && !file.webkitRelativePath) throw new Error('此浏览器未提供文件夹路径，请使用电脑浏览器拖拽文件夹上传')
    const relativePath = file.webkitRelativePath || file.name, components = relativePath.split('/')
    for (let index = 1; index < components.length; index++) {
      const directory = components.slice(0, index).join('/')
      if (!directories.has(directory)) { directories.add(directory); add({ path: directory, directory: true }) }
    }
    add({ path: relativePath, directory: false, file })
  }
  return entries
}
