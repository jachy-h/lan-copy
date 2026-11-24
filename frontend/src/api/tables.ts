import { requestJSON } from './client'

export interface TableValue {
  [column: string]: string
}

export interface TableRow {
  _row: number
  values: TableValue
}

export interface TableQuery {
  path: string
  q?: string
  filters?: Array<{ column: string; op: string; value?: string }>
  sort?: { column: string; order?: 'asc' | 'desc' }
  limit?: number
  offset?: number
}

export interface TableLayout {
  version: number
  order: string[]
  hidden: string[]
}

export interface TableQueryResult {
  path: string
  version: string
  columns: string[]
  rows: TableRow[]
  total: number
  offset: number
  limit: number
  modified: string
  layout?: TableLayout
}

export interface TableOperation {
  kind: 'insert' | 'update' | 'delete'
  key?: string
  values?: TableValue
  changes?: TableValue
}

export interface TableUpdate {
  path: string
  expectedVersion: string
  keyColumn?: string
  operations: TableOperation[]
}

export interface TableUpdateResult {
  path: string
  version: string
  applied: { inserted: number; updated: number; deleted: number }
}

const writeHeaders = { 'X-Lan-Copy-Action': 'tables' }

export function setTableMark(path: string, marked: boolean) {
  return requestJSON<{ path: string; marked: boolean }>('/api/tables/mark', {
    method: 'POST',
    headers: { ...writeHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, marked }),
  })
}

export function queryTable(input: TableQuery, signal?: AbortSignal) {
  return requestJSON<TableQueryResult>('/api/tables/query', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
    cache: 'no-store',
    signal,
  })
}

export function saveTableLayout(input: { path: string; order: string[]; hidden: string[] }) {
  return requestJSON<{ path: string; layout: TableLayout }>('/api/tables/layout', {
    method: 'POST',
    headers: { ...writeHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify({ version: 1, ...input }),
  })
}

export function createTable(path: string, columns: string[]) {
  return requestJSON<{ path: string; version: string; columns: string[]; modified: string }>('/api/tables/create', {
    method: 'POST',
    headers: { ...writeHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, columns }),
  })
}

export function lockAndUpdateTable(input: TableUpdate) {
  return requestJSON<TableUpdateResult>('/api/tables/lock-and-update', {
    method: 'POST',
    headers: { ...writeHeaders, 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}
