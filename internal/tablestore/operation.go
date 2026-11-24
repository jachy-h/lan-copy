package tablestore

import (
	"fmt"
	"strings"
)

type Operation struct {
	Kind    string            `json:"kind"`
	Key     string            `json:"key,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
	Changes map[string]string `json:"changes,omitempty"`
}

type UpdateRequest struct {
	ExpectedVersion string      `json:"expectedVersion"`
	KeyColumn       string      `json:"keyColumn,omitempty"`
	Operations      []Operation `json:"operations"`
}

type Applied struct {
	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	Deleted  int `json:"deleted"`
}

// Apply validates and applies all operations to an in-memory copy. Callers
// only persist the document when Apply returns without error.
func (d *Document) Apply(request UpdateRequest) (Applied, error) {
	var applied Applied
	if len(request.Operations) == 0 {
		return applied, fmt.Errorf("%w: no operations", ErrInvalidQuery)
	}
	if len(request.Operations) > MaxOperations {
		return applied, fmt.Errorf("%w: %d", ErrTooManyOperations, len(request.Operations))
	}
	columnIndex := make(map[string]int, len(d.Columns))
	for index, name := range d.Columns {
		columnIndex[strings.ToLower(name)] = index
	}
	keyIndex := -1
	if request.KeyColumn != "" {
		index, ok := columnIndex[strings.ToLower(request.KeyColumn)]
		if !ok {
			return applied, fmt.Errorf("%w: %s", ErrKeyMissing, request.KeyColumn)
		}
		keyIndex = index
	}
	needsKey := false
	for _, operation := range request.Operations {
		if operation.Kind == "update" || operation.Kind == "delete" {
			needsKey = true
		}
	}
	if needsKey && keyIndex < 0 {
		return applied, ErrKeyRequired
	}
	rows := make([][]string, len(d.Rows))
	for index, row := range d.Rows {
		rows[index] = append([]string(nil), row...)
	}
	keyIndexMap := make(map[string]int)
	if keyIndex >= 0 {
		for index, row := range rows {
			value := row[keyIndex]
			if value == "" {
				return applied, fmt.Errorf("%w: row %d", ErrKeyDuplicate, index+1)
			}
			if _, exists := keyIndexMap[value]; exists {
				return applied, fmt.Errorf("%w: %q", ErrKeyDuplicate, value)
			}
			keyIndexMap[value] = index
		}
	}
	for operationIndex, operation := range request.Operations {
		switch operation.Kind {
		case "insert":
			row, err := buildInsertRow(d.Columns, columnIndex, operation.Values)
			if err != nil {
				return applied, fmt.Errorf("operation %d: %w", operationIndex+1, err)
			}
			if keyIndex >= 0 {
				key := row[keyIndex]
				if key == "" {
					return applied, fmt.Errorf("operation %d: %w", operationIndex+1, ErrKeyDuplicate)
				}
				if _, exists := keyIndexMap[key]; exists {
					return applied, fmt.Errorf("operation %d: %w: %q", operationIndex+1, ErrKeyDuplicate, key)
				}
				keyIndexMap[key] = len(rows)
			}
			rows = append(rows, row)
			applied.Inserted++
			if len(rows) > MaxRows {
				return Applied{}, fmt.Errorf("%w: %d rows", ErrTooManyRows, len(rows))
			}
		case "update":
			index, err := lookupKey(keyIndexMap, operationIndex, operation.Key)
			if err != nil {
				return Applied{}, err
			}
			for name, value := range operation.Changes {
				column, ok := columnIndex[strings.ToLower(name)]
				if !ok {
					return Applied{}, fmt.Errorf("operation %d: %w: %s", operationIndex+1, ErrUnknownColumn, name)
				}
				if column == keyIndex && value != rows[index][keyIndex] {
					return Applied{}, fmt.Errorf("operation %d: %w", operationIndex+1, ErrKeyChange)
				}
				if len(value) > MaxCellBytes {
					return Applied{}, fmt.Errorf("operation %d: %w", operationIndex+1, ErrCellTooLarge)
				}
				rows[index][column] = value
			}
			applied.Updated++
		case "delete":
			index, err := lookupKey(keyIndexMap, operationIndex, operation.Key)
			if err != nil {
				return Applied{}, err
			}
			rows = append(rows[:index], rows[index+1:]...)
			keyIndexMap = rebuildKeyIndex(rows, keyIndex)
			applied.Deleted++
		default:
			return Applied{}, fmt.Errorf("operation %d: %w: kind %q", operationIndex+1, ErrInvalidQuery, operation.Kind)
		}
	}
	d.Rows = rows
	return applied, nil
}

func buildInsertRow(columns []string, columnIndex map[string]int, values map[string]string) ([]string, error) {
	row := make([]string, len(columns))
	for name, value := range values {
		column, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownColumn, name)
		}
		if len(value) > MaxCellBytes {
			return nil, ErrCellTooLarge
		}
		row[column] = value
	}
	return row, nil
}

func lookupKey(index map[string]int, operationIndex int, key string) (int, error) {
	position, ok := index[key]
	if !ok {
		return 0, fmt.Errorf("operation %d: %w: %q", operationIndex+1, ErrKeyNotFound, key)
	}
	return position, nil
}

func rebuildKeyIndex(rows [][]string, keyIndex int) map[string]int {
	if keyIndex < 0 {
		return nil
	}
	index := make(map[string]int, len(rows))
	for position, row := range rows {
		index[row[keyIndex]] = position
	}
	return index
}
