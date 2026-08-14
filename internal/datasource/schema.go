package datasource

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func inferSchema(ds *Dataset) {
	for colIdx := range ds.Schema.Columns {
		typeCounts := map[DataType]int{TypeInt: 0, TypeFloat: 0, TypeString: 0, TypeDate: 0, TypeBool: 0}
		nullCount := 0
		totalRows := len(ds.Rows)

		for _, row := range ds.Rows {
			if colIdx >= len(row.Values) {
				nullCount++
				continue
			}
			cell := row.Values[colIdx]
			if cell.IsNull {
				nullCount++
				continue
			}
			detected := detectType(cell.Raw)
			typeCounts[detected]++
		}

		ds.Schema.Columns[colIdx].Nullable = nullCount > 0

		nonNull := totalRows - nullCount
		if nonNull == 0 {
			ds.Schema.Columns[colIdx].DataType = TypeString
			continue
		}

		bestType := TypeString
		bestCount := 0
		for dt, count := range typeCounts {
			if count > bestCount {
				bestCount = count
				bestType = dt
			}
		}

		if float64(bestCount)/float64(nonNull) >= 0.8 {
			ds.Schema.Columns[colIdx].DataType = bestType
		}

		if typeCounts[TypeInt] > 0 && typeCounts[TypeFloat] > 0 {
			if float64(typeCounts[TypeFloat])/float64(nonNull) > 0.1 {
				ds.Schema.Columns[colIdx].DataType = TypeFloat
			}
		}

		for rowIdx := range ds.Rows {
			if colIdx >= len(ds.Rows[rowIdx].Values) {
				continue
			}
			ds.Rows[rowIdx].Values[colIdx].Type = ds.Schema.Columns[colIdx].DataType
			convertCellValue(&ds.Rows[rowIdx].Values[colIdx])
		}
	}
}

func detectType(s string) DataType {
	if s == "" {
		return TypeUnknown
	}
	sl := strings.ToLower(s)
	if sl == "true" || sl == "false" {
		return TypeBool
	}

	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return TypeInt
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return TypeFloat
	}

	dateFormats := []string{
		"2006-01-02",
		"2006/01/02",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, f := range dateFormats {
		if _, err := time.Parse(f, s); err == nil {
			return TypeDate
		}
	}

	return TypeString
}

func convertCellValue(cell *CellValue) {
	if cell.IsNull || cell.Raw == "" {
		return
	}
	switch cell.Type {
	case TypeInt:
		if v, err := strconv.ParseInt(cell.Raw, 10, 64); err == nil {
			cell.IntVal = v
		}
	case TypeFloat:
		if v, err := strconv.ParseFloat(cell.Raw, 64); err == nil {
			cell.FloatVal = v
		}
	case TypeBool:
		sl := strings.ToLower(cell.Raw)
		cell.BoolVal = sl == "true" || sl == "1"
	case TypeDate:
		dateFormats := []string{
			"2006-01-02",
			"2006/01/02",
			"2006-01-02T15:04:05Z",
			"2006-01-02 15:04:05",
			time.RFC3339,
		}
		for _, f := range dateFormats {
			if t, err := time.Parse(f, cell.Raw); err == nil {
				cell.DateVal = t
				break
			}
		}
		cell.StrVal = cell.Raw
	case TypeString:
		cell.StrVal = cell.Raw
	}
}

func cellValueToInterface(cell CellValue) interface{} {
	if cell.IsNull {
		return nil
	}
	switch cell.Type {
	case TypeInt:
		return cell.IntVal
	case TypeFloat:
		if math.IsNaN(cell.FloatVal) || math.IsInf(cell.FloatVal, 0) {
			return nil
		}
		return cell.FloatVal
	case TypeBool:
		return cell.BoolVal
	case TypeDate:
		return cell.DateVal.Format("2006-01-02")
	default:
		return cell.StrVal
	}
}

func CellValueToString(cell CellValue) string {
	if cell.IsNull {
		return ""
	}
	switch cell.Type {
	case TypeInt:
		return strconv.FormatInt(cell.IntVal, 10)
	case TypeFloat:
		return strconv.FormatFloat(cell.FloatVal, 'f', -1, 64)
	case TypeBool:
		return strconv.FormatBool(cell.BoolVal)
	case TypeDate:
		return cell.DateVal.Format("2006-01-02")
	default:
		return cell.StrVal
	}
}

func CellValueToJSON(cell CellValue) interface{} {
	return cellValueToInterface(cell)
}

func RowToMap(row Row, schema Schema) map[string]interface{} {
	m := make(map[string]interface{})
	for i, col := range schema.Columns {
		if i < len(row.Values) {
			m[col.Name] = cellValueToInterface(row.Values[i])
		} else {
			m[col.Name] = nil
		}
	}
	return m
}

func init() {
	_ = json.Marshal
	_ = fmt.Sprintf
	_ = math.NaN
}
