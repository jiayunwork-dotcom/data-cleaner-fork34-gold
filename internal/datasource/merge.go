package datasource

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envVarRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

func resolveEnvVars(s string) string {
	return envVarRegex.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarRegex.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

func MergeSchemas(datasets []*Dataset) (*Schema, error) {
	if len(datasets) == 0 {
		return &Schema{}, nil
	}

	columnMap := make(map[string]ColumnSchema)
	columnOrder := make([]string, 0)

	for _, ds := range datasets {
		for _, col := range ds.Schema.Columns {
			existing, found := columnMap[col.Name]
			if !found {
				columnMap[col.Name] = col
				columnOrder = append(columnOrder, col.Name)
			} else {
				if existing.DataType != col.DataType && col.DataType != TypeUnknown && existing.DataType != TypeUnknown {
					return nil, fmt.Errorf("schema conflict: column '%s' has type %s in one source but %s in another",
						col.Name, existing.DataType, col.DataType)
				}
				if col.DataType != TypeUnknown && existing.DataType == TypeUnknown {
					existing.DataType = col.DataType
				}
				if col.Nullable {
					existing.Nullable = true
				}
				columnMap[col.Name] = existing
			}
		}
	}

	merged := &Schema{Columns: make([]ColumnSchema, len(columnOrder))}
	for i, name := range columnOrder {
		merged.Columns[i] = columnMap[name]
	}
	return merged, nil
}

func MergeDatasets(datasets []*Dataset) (*Dataset, error) {
	if len(datasets) == 0 {
		return &Dataset{}, nil
	}

	mergedSchema, err := MergeSchemas(datasets)
	if err != nil {
		return nil, err
	}

	nameIndex := make(map[string]int)
	for i, col := range mergedSchema.Columns {
		nameIndex[col.Name] = i
	}

	merged := &Dataset{
		Name:   "merged",
		Schema: *mergedSchema,
		Rows:   make([]Row, 0),
	}

	for _, ds := range datasets {
		sourceIndex := make(map[string]int)
		for i, col := range ds.Schema.Columns {
			sourceIndex[col.Name] = i
		}

		for _, srcRow := range ds.Rows {
			newRow := Row{Values: make([]CellValue, len(mergedSchema.Columns))}
			for i := range newRow.Values {
				newRow.Values[i] = CellValue{IsNull: true, Type: TypeUnknown}
			}
			for colName, srcIdx := range sourceIndex {
				tgtIdx := nameIndex[colName]
				if srcIdx < len(srcRow.Values) {
					newRow.Values[tgtIdx] = srcRow.Values[srcIdx]
				}
			}
			merged.Rows = append(merged.Rows, newRow)
		}
	}

	for colIdx := range merged.Schema.Columns {
		hasNull := false
		for _, row := range merged.Rows {
			if colIdx >= len(row.Values) || row.Values[colIdx].IsNull {
				hasNull = true
				break
			}
		}
		if hasNull {
			merged.Schema.Columns[colIdx].Nullable = true
		}
	}

	return merged, nil
}

func DatasetFromMap(name string, records []map[string]interface{}) (*Dataset, error) {
	if len(records) == 0 {
		return &Dataset{Name: name}, nil
	}

	columnSet := make(map[string]bool)
	for _, rec := range records {
		for k := range rec {
			columnSet[k] = true
		}
	}

	columns := make([]string, 0, len(columnSet))
	for k := range columnSet {
		columns = append(columns, k)
	}

	ds := &Dataset{
		Name: name,
		Schema: Schema{
			Columns: make([]ColumnSchema, len(columns)),
		},
		Rows: make([]Row, 0, len(records)),
	}

	for i, col := range columns {
		ds.Schema.Columns[i] = ColumnSchema{
			Name:     col,
			DataType: TypeString,
			Nullable: true,
		}
	}

	for _, rec := range records {
		row := Row{Values: make([]CellValue, len(columns))}
		for i, col := range columns {
			val, exists := rec[col]
			if !exists || val == nil {
				row.Values[i] = CellValue{IsNull: true, Type: TypeUnknown}
			} else {
				row.Values[i] = interfaceToCellValue(val)
			}
		}
		ds.Rows = append(ds.Rows, row)
	}

	inferSchema(ds)
	return ds, nil
}

func FormatCellValue(cell CellValue) string {
	if cell.IsNull {
		return "NULL"
	}
	switch cell.Type {
	case TypeInt:
		return fmt.Sprintf("%d", cell.IntVal)
	case TypeFloat:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", cell.FloatVal), "0"), ".")
	case TypeBool:
		return fmt.Sprintf("%t", cell.BoolVal)
	case TypeDate:
		return cell.DateVal.Format("2006-01-02")
	default:
		return cell.StrVal
	}
}
