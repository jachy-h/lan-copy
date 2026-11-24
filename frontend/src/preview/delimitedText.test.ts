import { describe, expect, it } from 'vitest'
import { parseDelimited } from './delimitedText'

describe('parseDelimited', () => {
  it('uses the first record as the header and reads rows', () => {
    const table = parseDelimited('id,name,status\n1,Alice,new\n2,Bob,done\n', ',')
    expect(table.header).toEqual(['id', 'name', 'status'])
    expect(table.rows).toEqual([['1', 'Alice', 'new'], ['2', 'Bob', 'done']])
    expect(table.truncatedRows).toBe(false)
    expect(table.truncatedColumns).toBe(false)
    expect(table.malformed).toBe(false)
  })

  it('handles quoted fields, escaped quotes and embedded delimiters', () => {
    const table = parseDelimited('id,remark\n1,"a, b"\n2,"he said ""hi"""\n', ',')
    expect(table.rows).toEqual([['1', 'a, b'], ['2', 'he said "hi"']])
  })

  it('normalizes CRLF and keeps line breaks inside quoted fields', () => {
    const table = parseDelimited('id,note\r\n1,"line one\r\nline two"\r\n2,plain\r\n', ',')
    expect(table.rows).toEqual([['1', 'line one\nline two'], ['2', 'plain']])
  })

  it('strips a UTF-8 BOM and skips blank lines', () => {
    const table = parseDelimited('\ufeffid,name\n\n1,Alice\n\n', ',')
    expect(table.header).toEqual(['id', 'name'])
    expect(table.rows).toEqual([['1', 'Alice']])
  })

  it('pads short rows and keeps extra cells up to the column limit', () => {
    const table = parseDelimited('id,name\n1\n2,Alice,extra\n', ',')
    expect(table.header).toEqual(['id', 'name', ''])
    expect(table.rows).toEqual([['1', '', ''], ['2', 'Alice', 'extra']])
  })

  it('supports a tab delimiter and CR line endings', () => {
    const table = parseDelimited('id\tname\r1\tAlice\r', '\t')
    expect(table.header).toEqual(['id', 'name'])
    expect(table.rows).toEqual([['1', 'Alice']])
  })

  it('reports row and column truncation at the configured limits', () => {
    const table = parseDelimited('a,b,c\n1,2,3\n4,5,6\n7,8,9\n', ',', { maxRows: 2, maxColumns: 2 })
    expect(table.header).toEqual(['a', 'b'])
    expect(table.rows).toEqual([['1', '2'], ['4', '5']])
    expect(table.truncatedRows).toBe(true)
    expect(table.truncatedColumns).toBe(true)
  })

  it('returns an empty table for empty or whitespace-only input', () => {
    expect(parseDelimited('', ',')).toMatchObject({ header: [], rows: [] })
    expect(parseDelimited('\n\r\n', ',')).toMatchObject({ header: [], rows: [] })
  })

  it('flags an unterminated quoted field without throwing', () => {
    const table = parseDelimited('id,note\n1,"unfinished', ',')
    expect(table.header).toEqual(['id', 'note'])
    expect(table.rows).toEqual([['1', 'unfinished']])
    expect(table.malformed).toBe(true)
  })

  it('keeps a header made only of empty quoted fields', () => {
    const table = parseDelimited('""\nA\n', ',')
    expect(table.header).toEqual([''])
    expect(table.rows).toEqual([['A']])
  })
})
