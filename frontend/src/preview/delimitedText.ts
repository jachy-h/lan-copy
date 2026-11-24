export interface DelimitedTable {
  header: string[]
  rows: string[][]
  truncatedRows: boolean
  truncatedColumns: boolean
  malformed: boolean
}

export interface DelimitedOptions {
  maxRows?: number
  maxColumns?: number
}

const defaultMaxRows = 250
const defaultMaxColumns = 50

// parseDelimited reads RFC 4180 style CSV/TSV text for read-only preview.
// The first record is the header; quoted fields may contain the delimiter,
// escaped quotes ("") and line breaks. CRLF inside a quoted field becomes LF,
// matching Go's encoding/csv used by the smart-table store.
export function parseDelimited(text: string, delimiter: string, options: DelimitedOptions = {}): DelimitedTable {
  const maxRows = options.maxRows ?? defaultMaxRows
  const maxColumns = options.maxColumns ?? defaultMaxColumns
  const source = text.charCodeAt(0) === 0xfeff ? text.slice(1) : text
  const records: string[][] = []
  let field = ''
  let record: string[] = []
  let recordStarted = false
  let quoted = false
  let truncatedRows = false
  let index = 0

  const finishField = () => {
    record.push(field)
    field = ''
  }
  const finishRecord = () => {
    finishField()
    const wasStarted = recordStarted
    recordStarted = false
    // Blank lines carry no data and are skipped, like encoding/csv.
    if (!wasStarted && record.length === 1 && record[0] === '') {
      record = []
      return
    }
    if (records.length > maxRows) {
      truncatedRows = true
      record = []
      return
    }
    records.push(record)
    record = []
  }

  while (index < source.length) {
    const char = source[index]
    if (quoted) {
      if (char === '"') {
        if (source[index + 1] === '"') {
          field += '"'
          index += 2
          continue
        }
        quoted = false
        index++
        continue
      }
      if (char === '\r') {
        field += '\n'
        index += source[index + 1] === '\n' ? 2 : 1
        continue
      }
      field += char
      index++
      continue
    }
    if (char === '"' && field === '') {
      quoted = true
      recordStarted = true
      index++
      continue
    }
    if (char === delimiter) {
      recordStarted = true
      finishField()
      index++
      continue
    }
    if (char === '\r' || char === '\n') {
      index += char === '\r' && source[index + 1] === '\n' ? 2 : 1
      finishRecord()
      if (truncatedRows) break
      continue
    }
    recordStarted = true
    field += char
    index++
  }

  const malformed = quoted && index >= source.length
  if (!truncatedRows && (field !== '' || record.length > 0 || quoted)) finishRecord()

  let columnCount = 0
  let truncatedColumns = false
  for (const row of records) {
    if (row.length > maxColumns) truncatedColumns = true
    columnCount = Math.max(columnCount, Math.min(row.length, maxColumns))
  }
  const headerRecord = records[0] || []
  const header = Array.from({ length: columnCount }, (_, column) => headerRecord[column] ?? '')
  const rows = records.slice(1).map(row => Array.from({ length: columnCount }, (_, column) => row[column] ?? ''))
  return { header, rows, truncatedRows, truncatedColumns, malformed }
}
