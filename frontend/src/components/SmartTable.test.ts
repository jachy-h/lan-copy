import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SmartTable from './SmartTable.vue'
import * as api from '../api/tables'

vi.mock('../api/tables', async importOriginal => {
  const actual = await importOriginal<typeof import('../api/tables')>()
  return {
    ...actual,
    queryTable: vi.fn(),
    lockAndUpdateTable: vi.fn(),
    saveTableLayout: vi.fn(),
  }
})

const path = 'data/orders.csv'
const result = {
  path,
  version: 'sha256:version-1',
  columns: ['id', 'status'],
  rows: [{ _row: 1, values: { id: 'A-1', status: 'new' } }],
  total: 1,
  offset: 0,
  limit: 50,
  modified: '2026-11-19T00:00:00Z',
}

let wrapper: ReturnType<typeof mount>

beforeEach(() => {
  vi.mocked(api.queryTable).mockResolvedValue(result)
  vi.mocked(api.lockAndUpdateTable).mockResolvedValue({
    path, version: 'sha256:version-2',
    applied: { inserted: 0, updated: 1, deleted: 0 },
  })
  vi.mocked(api.saveTableLayout).mockImplementation(async input => ({
    path,
    layout: { version: 1, order: input.order, hidden: input.hidden },
  }))
})

afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})

function mountTable() {
  wrapper = mount(SmartTable, { props: { path } })
  return wrapper
}

describe('SmartTable', () => {
  it('loads only the requested CSV file and shows query results', async () => {
    const view = mountTable()
    await flushPromises()
    expect(api.queryTable).toHaveBeenCalledWith(expect.objectContaining({ path, limit: 50 }), expect.anything())
    expect(view.text()).toContain('orders.csv')
    expect(view.text()).toContain('data/orders.csv')
    expect(view.text()).toContain('A-1')
    expect(view.text()).toContain('sha256:version-1')
    expect(view.find('.tables-sidebar').exists()).toBe(false)
    expect(view.text()).not.toContain('新建表格')
  })

  it('updates a row with the current version and key column', async () => {
    const view = mountTable()
    await flushPromises()
    const edit = view.findAll('button').find(button => button.text() === '编辑')!
    await edit.trigger('click')
    const statusInput = view.find('input[aria-label="status 第 1 行"]')
    await statusInput.setValue('done')
    const save = view.findAll('button').find(button => button.text() === '保存')!
    await save.trigger('click')
    await flushPromises()
    expect(api.lockAndUpdateTable).toHaveBeenCalledWith({
      path,
      expectedVersion: 'sha256:version-1',
      keyColumn: 'id',
      operations: [{ kind: 'update', key: 'A-1', changes: { status: 'done' } }],
    })
  })

  it('searches all columns after a short debounce and highlights matches', async () => {
    const view = mountTable()
    await flushPromises()
    const search = view.find('input[aria-label="搜索全部列"]')
    await search.setValue('ne')
    await new Promise(resolve => setTimeout(resolve, 350))
    await flushPromises()
    const lastCall = vi.mocked(api.queryTable).mock.calls.at(-1)![0]
    expect(lastCall).toMatchObject({ path, q: 'ne' })
    expect(lastCall.filters).toBeUndefined()
    expect(view.findAll('mark.tables-highlight').some(mark => mark.text() === 'ne')).toBe(true)
  })

  it('emits close and preview actions from the inline toolbar', async () => {
    const view = mountTable()
    await flushPromises()
    await view.find('#tableBack').trigger('click')
    expect(view.emitted('close')).toHaveLength(1)
    await view.findAll('button').find(button => button.text() === '预览原文件')!.trigger('click')
    expect(view.emitted('preview')).toHaveLength(1)
  })

  it('applies saved column order and hides columns', async () => {
    vi.mocked(api.queryTable).mockResolvedValue({
      ...result,
      columns: ['id', 'status', 'note'],
      layout: { version: 1, order: ['status', 'id', 'note'], hidden: ['note'] },
    })
    const view = mountTable()
    await flushPromises()
    const headers = view.findAll('thead th').map(header => header.text())
    expect(headers).toEqual(['#', 'status', 'id', '操作'])
  })

  it('saves a new column order', async () => {
    const view = mountTable()
    await flushPromises()
    await view.findAll('button').find(button => button.text() === '列设置')!.trigger('click')
    await view.find('button[aria-label="下移 id"]').trigger('click')
    await flushPromises()
    expect(api.saveTableLayout).toHaveBeenCalledWith({
      path,
      order: ['status', 'id'],
      hidden: [],
    })
    expect(view.findAll('thead th').map(header => header.text())).toEqual(['#', 'status', 'id', '操作'])
  })

  it('hides a column and saves the layout', async () => {
    const view = mountTable()
    await flushPromises()
    await view.findAll('button').find(button => button.text() === '列设置')!.trigger('click')
    await view.find('input[aria-label="显示 status"]').setValue(false)
    await flushPromises()
    expect(api.saveTableLayout).toHaveBeenCalledWith({
      path,
      order: ['id', 'status'],
      hidden: ['status'],
    })
    expect(view.findAll('thead th').map(header => header.text())).toEqual(['#', 'id', '操作'])
  })

  it('allows hiding the record id but keeps it available when adding a row', async () => {
    const view = mountTable()
    await flushPromises()
    await view.findAll('button').find(button => button.text() === '列设置')!.trigger('click')
    const idCheckbox = view.find('input[aria-label="显示 id"]')
    expect(idCheckbox.attributes('disabled')).toBeUndefined()
    await idCheckbox.setValue(false)
    await flushPromises()
    expect(view.findAll('thead th').map(header => header.text())).toEqual(['#', 'status', '操作'])
    await view.findAll('button').find(button => button.text() === '新增行')!.trigger('click')
    expect(view.find('input[aria-label="新行 id"]').exists()).toBe(true)
    expect(view.find('input[aria-label="新行 status"]').exists()).toBe(true)
  })

  it('restores every column with one action', async () => {
    vi.mocked(api.queryTable).mockResolvedValue({
      ...result,
      columns: ['id', 'status', 'note'],
      layout: { version: 1, order: ['id', 'status', 'note'], hidden: ['note'] },
    })
    const view = mountTable()
    await flushPromises()
    await view.findAll('button').find(button => button.text() === '列设置')!.trigger('click')
    await view.findAll('button').find(button => button.text() === '全部显示')!.trigger('click')
    await flushPromises()
    expect(api.saveTableLayout).toHaveBeenCalledWith(expect.objectContaining({
      order: ['id', 'status', 'note'],
      hidden: [],
    }))
    expect(view.findAll('thead th').map(header => header.text())).toEqual(['#', 'id', 'status', 'note', '操作'])
  })

  it('keeps the table usable when layout saving fails', async () => {
    vi.mocked(api.saveTableLayout).mockRejectedValue(new Error('偏好保存失败'))
    const view = mountTable()
    await flushPromises()
    await view.findAll('button').find(button => button.text() === '列设置')!.trigger('click')
    await view.find('button[aria-label="下移 id"]').trigger('click')
    await flushPromises()
    expect(view.text()).toContain('偏好保存失败')
    expect(view.text()).toContain('A-1')
  })
})
