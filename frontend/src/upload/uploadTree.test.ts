import { describe, expect, it, vi } from 'vitest'
import { fromEntries, fromFiles } from './uploadTree'

function fileEntry(name: string): FileSystemFileEntry {
  return { name, isFile: true, isDirectory: false, file(resolve) { queueMicrotask(() => resolve(new File([name], name))) } } as FileSystemFileEntry
}
function directoryEntry(name: string, children: FileSystemEntry[]): FileSystemDirectoryEntry {
  return { name, isFile: false, isDirectory: true, createReader() { let index = 0; return { readEntries(resolve) { const batch = children.slice(index, index + 100); index += 100; queueMicrotask(() => resolve(batch)) } } } } as FileSystemDirectoryEntry
}

describe('upload tree collection', () => {
  it('reads every directory page, keeps empty folders, nests paths, and reports progress', async () => {
    const children: FileSystemEntry[] = Array.from({ length: 205 }, (_, index) => fileEntry(`${index}.txt`))
    children.push(directoryEntry('empty', []), directoryEntry('nested', [fileEntry('same.txt')]))
    const progress = vi.fn()
    const entries = await fromEntries([directoryEntry('照片', children), fileEntry('loose.txt')], progress)
    expect(entries.filter(entry => !entry.directory)).toHaveLength(207)
    expect(entries).toContainEqual({ path: '照片/empty', directory: true })
    expect(entries.some(entry => entry.path === '照片/204.txt')).toBe(true)
    expect(entries.some(entry => entry.path === '照片/nested/same.txt')).toBe(true)
    expect(entries.some(entry => entry.path === 'loose.txt')).toBe(true)
    expect(progress).toHaveBeenCalledWith(1)
    expect(progress).toHaveBeenCalledWith(50)
  })

  it('preserves picker folders and enforces relative path, count, depth, and read errors', async () => {
    const first = new File(['a'], 'a.txt'); Object.defineProperty(first, 'webkitRelativePath', { value: 'folder/sub/a.txt' })
    const second = new File(['b'], 'b.txt'); Object.defineProperty(second, 'webkitRelativePath', { value: 'folder/sub/b.txt' })
    const picked = fromFiles([first, second], true)
    expect(picked.filter(entry => entry.directory).map(entry => entry.path)).toEqual(['folder', 'folder/sub'])
    expect(() => fromFiles([new File(['x'], 'a.txt')], true)).toThrow(/未提供文件夹路径/)
    expect(() => fromFiles(Array.from({ length: 10_001 }, (_, index) => new File(['x'], `${index}.txt`)))).toThrow(/10000/)
    await expect(fromEntries([{
      name: 'denied', isFile: true, isDirectory: false,
      file(_resolve, reject) { reject?.(new DOMException('permission denied')) },
    } as FileSystemFileEntry])).rejects.toThrow('permission denied')
    let deep: FileSystemEntry = fileEntry('file.txt')
    for (let index = 0; index < 65; index++) deep = directoryEntry('sub', [deep])
    await expect(fromEntries([deep])).rejects.toThrow(/64/)
  })
})
