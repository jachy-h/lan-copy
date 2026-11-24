// Package tablestore owns smart-table CSV parsing, querying and row updates.
// It relies on filestore for path policy, locking and atomic writes.
package tablestore

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	MaxFileBytes  = 8 << 20
	MaxRows       = 50000
	MaxColumns    = 200
	MaxCellBytes  = 64 << 10
	MaxQueryRows  = 500
	MaxOperations = 1000
)

var (
	ErrNotCSV            = errors.New("file is not a csv table")
	ErrEmptyTable        = errors.New("csv has no header")
	ErrInvalidCSV        = errors.New("invalid csv")
	ErrTooLarge          = errors.New("table exceeds size limit")
	ErrTooManyRows       = errors.New("table has too many rows")
	ErrTooManyColumns    = errors.New("table has too many columns")
	ErrCellTooLarge      = errors.New("table cell exceeds size limit")
	ErrVersionConflict   = errors.New("table version conflict")
	ErrKeyRequired       = errors.New("key column is required")
	ErrKeyMissing        = errors.New("key column does not exist")
	ErrKeyDuplicate      = errors.New("key column has duplicate or empty values")
	ErrKeyNotFound       = errors.New("key was not found")
	ErrKeyChange         = errors.New("key column cannot be changed")
	ErrUnknownColumn     = errors.New("unknown column")
	ErrTooManyOperations = errors.New("too many table operations")
	ErrInvalidQuery      = errors.New("invalid table query")
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Document is one parsed CSV table. Rows are aligned with Columns.
type Document struct {
	Columns []string
	Rows    [][]string
	bom     bool
	crlf    bool
}

// Parse decodes a CSV file. The first record is the header; ragged rows,
// duplicate headers and empty headers are rejected explicitly.
func Parse(data []byte) (*Document, error) {
	if int64(len(data)) > MaxFileBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	bom := bytes.HasPrefix(data, utf8BOM)
	if bom {
		data = data[len(utf8BOM):]
	}
	crlf := detectCRLF(data)
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = false
	reader.TrimLeadingSpace = false
	records := make([][]string, 0, 64)
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCSV, err)
		}
		records = append(records, record)
		if len(records) > MaxRows+1 {
			return nil, fmt.Errorf("%w: more than %d rows", ErrTooManyRows, MaxRows)
		}
	}
	if len(records) == 0 {
		return nil, ErrEmptyTable
	}
	header := records[0]
	if len(header) == 0 {
		return nil, ErrEmptyTable
	}
	if len(header) > MaxColumns {
		return nil, fmt.Errorf("%w: %d columns", ErrTooManyColumns, len(header))
	}
	seen := make(map[string]struct{}, len(header))
	for index, name := range header {
		if name == "" {
			return nil, fmt.Errorf("%w: column %d has an empty name", ErrInvalidCSV, index+1)
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate column %q", ErrInvalidCSV, name)
		}
		seen[key] = struct{}{}
		if len(name) > MaxCellBytes {
			return nil, fmt.Errorf("%w: column %d", ErrCellTooLarge, index+1)
		}
	}
	rows := make([][]string, 0, len(records)-1)
	for rowIndex, record := range records[1:] {
		if len(record) != len(header) {
			return nil, fmt.Errorf("%w: row %d has %d fields, want %d", ErrInvalidCSV, rowIndex+1, len(record), len(header))
		}
		for column, value := range record {
			if len(value) > MaxCellBytes {
				return nil, fmt.Errorf("%w: row %d column %d", ErrCellTooLarge, rowIndex+1, column+1)
			}
		}
		rows = append(rows, record)
	}
	if len(rows) > MaxRows {
		return nil, fmt.Errorf("%w: %d rows", ErrTooManyRows, len(rows))
	}
	return &Document{Columns: header, Rows: rows, bom: bom, crlf: crlf}, nil
}

func detectCRLF(data []byte) bool {
	crlf := bytes.Count(data, []byte("\r\n"))
	if crlf == 0 {
		return false
	}
	return crlf == bytes.Count(data, []byte("\n"))
}

// Marshal encodes the document as UTF-8 CSV, preserving BOM and line endings.
func (d *Document) Marshal() ([]byte, error) {
	var buffer bytes.Buffer
	if d.bom {
		buffer.Write(utf8BOM)
	}
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = d.crlf
	if err := writer.Write(d.Columns); err != nil {
		return nil, err
	}
	for _, row := range d.Rows {
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	return data, nil
}

// NewDocument creates a table with a header and no rows.
func NewDocument(columns []string) (*Document, error) {
	if len(columns) == 0 {
		return nil, ErrEmptyTable
	}
	if len(columns) > MaxColumns {
		return nil, fmt.Errorf("%w: %d columns", ErrTooManyColumns, len(columns))
	}
	seen := make(map[string]struct{}, len(columns))
	for index, name := range columns {
		if name == "" || len(name) > MaxCellBytes {
			return nil, fmt.Errorf("%w: column %d", ErrInvalidCSV, index+1)
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate column %q", ErrInvalidCSV, name)
		}
		seen[key] = struct{}{}
	}
	return &Document{Columns: append([]string(nil), columns...), Rows: [][]string{}}, nil
}

// Row is one query result. Values is keyed by the table's column names.
type Row struct {
	Number int               `json:"_row"`
	Values map[string]string `json:"values"`
}

type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
}

type Sort struct {
	Column string `json:"column"`
	Order  string `json:"order,omitempty"`
}

type Query struct {
	Q       string   `json:"q,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
	Sort    *Sort    `json:"sort,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Offset  int      `json:"offset,omitempty"`
}

type Result struct {
	Columns []string `json:"columns"`
	Rows    []Row    `json:"rows"`
	Total   int      `json:"total"`
	Offset  int      `json:"offset"`
	Limit   int      `json:"limit"`
}

type indexedRow struct {
	number int
	values []string
}

func (d *Document) Query(query Query) (Result, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxQueryRows {
		limit = MaxQueryRows
	}
	if query.Offset < 0 || query.Offset > 1000000 {
		return Result{}, fmt.Errorf("%w: offset out of range", ErrInvalidQuery)
	}
	columnIndex := make(map[string]int, len(d.Columns))
	for index, name := range d.Columns {
		columnIndex[strings.ToLower(name)] = index
	}
	for _, filter := range query.Filters {
		if _, ok := columnIndex[strings.ToLower(filter.Column)]; !ok {
			return Result{}, fmt.Errorf("%w: %s", ErrUnknownColumn, filter.Column)
		}
		if !validFilterOp(filter.Op) {
			return Result{}, fmt.Errorf("%w: %s", ErrInvalidQuery, filter.Op)
		}
	}
	var sortIndex = -1
	descending := false
	if query.Sort != nil {
		index, ok := columnIndex[strings.ToLower(query.Sort.Column)]
		if !ok {
			return Result{}, fmt.Errorf("%w: %s", ErrUnknownColumn, query.Sort.Column)
		}
		sortIndex = index
		descending = strings.EqualFold(query.Sort.Order, "desc")
	}
	needle := strings.ToLower(strings.TrimSpace(query.Q))
	matched := make([]indexedRow, 0, len(d.Rows))
	for index, values := range d.Rows {
		if needle != "" {
			found := false
			for _, value := range values {
				if strings.Contains(strings.ToLower(value), needle) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if !matchesFilters(values, columnIndex, query.Filters) {
			continue
		}
		matched = append(matched, indexedRow{number: index + 1, values: values})
	}
	if sortIndex >= 0 {
		sort.SliceStable(matched, func(left, right int) bool {
			a, b := matched[left].values[sortIndex], matched[right].values[sortIndex]
			comparison := compareValues(a, b)
			if descending {
				return comparison > 0
			}
			return comparison < 0
		})
	}
	total := len(matched)
	if query.Offset >= len(matched) {
		matched = nil
	} else {
		matched = matched[query.Offset:]
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}
	rows := make([]Row, 0, len(matched))
	for _, item := range matched {
		values := make(map[string]string, len(d.Columns))
		for index, name := range d.Columns {
			values[name] = item.values[index]
		}
		rows = append(rows, Row{Number: item.number, Values: values})
	}
	return Result{Columns: append([]string(nil), d.Columns...), Rows: rows, Total: total, Offset: query.Offset, Limit: limit}, nil
}

func matchesFilters(values []string, columns map[string]int, filters []Filter) bool {
	for _, filter := range filters {
		index := columns[strings.ToLower(filter.Column)]
		value := values[index]
		switch filter.Op {
		case "eq":
			if value != filter.Value {
				return false
			}
		case "neq":
			if value == filter.Value {
				return false
			}
		case "contains":
			if !strings.Contains(strings.ToLower(value), strings.ToLower(filter.Value)) {
				return false
			}
		case "prefix":
			if !strings.HasPrefix(strings.ToLower(value), strings.ToLower(filter.Value)) {
				return false
			}
		case "gt":
			if compareValues(value, filter.Value) <= 0 {
				return false
			}
		case "gte":
			if compareValues(value, filter.Value) < 0 {
				return false
			}
		case "lt":
			if compareValues(value, filter.Value) >= 0 {
				return false
			}
		case "lte":
			if compareValues(value, filter.Value) > 0 {
				return false
			}
		case "empty":
			if value != "" {
				return false
			}
		case "not_empty":
			if value == "" {
				return false
			}
		}
	}
	return true
}

func validFilterOp(op string) bool {
	switch op {
	case "eq", "neq", "contains", "prefix", "gt", "gte", "lt", "lte", "empty", "not_empty":
		return true
	}
	return false
}

func compareValues(a, b string) int {
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(a), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if leftErr == nil && rightErr == nil {
		switch {
		case left < right:
			return -1
		case left > right:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}
