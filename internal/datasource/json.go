package datasource

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func flattenJSON(obj map[string]interface{}, prefix string, result map[string]interface{}) {
	for k, v := range obj {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch child := v.(type) {
		case map[string]interface{}:
			flattenJSON(child, fullKey, result)
		case []interface{}:
			jsonBytes, _ := json.Marshal(child)
			result[fullKey] = string(jsonBytes)
		default:
			result[fullKey] = v
		}
	}
}

func ReadJSON(filepath string) (*Dataset, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("read json: %w", err)
	}

	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	var records []map[string]interface{}

	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if obj, ok := item.(map[string]interface{}); ok {
				flat := make(map[string]interface{})
				flattenJSON(obj, "", flat)
				records = append(records, flat)
			}
		}
	case map[string]interface{}:
		if dataArr, ok := v["data"].([]interface{}); ok {
			for _, item := range dataArr {
				if obj, ok := item.(map[string]interface{}); ok {
					flat := make(map[string]interface{})
					flattenJSON(obj, "", flat)
					records = append(records, flat)
				}
			}
		} else {
			flat := make(map[string]interface{})
			flattenJSON(v, "", flat)
			records = append(records, flat)
		}
	default:
		return nil, fmt.Errorf("unsupported json structure")
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no records found in json")
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
		Name: filepath,
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
				continue
			}
			row.Values[i] = interfaceToCellValue(val)
		}
		ds.Rows = append(ds.Rows, row)
	}

	inferSchema(ds)
	return ds, nil
}

func interfaceToCellValue(v interface{}) CellValue {
	switch val := v.(type) {
	case float64:
		if val == float64(int64(val)) {
			return CellValue{IntVal: int64(val), FloatVal: val, Type: TypeInt, Raw: fmt.Sprintf("%d", int64(val))}
		}
		return CellValue{FloatVal: val, Type: TypeFloat, Raw: strconv.FormatFloat(val, 'f', -1, 64)}
	case string:
		return CellValue{StrVal: val, Raw: val, Type: TypeString}
	case bool:
		return CellValue{BoolVal: val, Type: TypeBool, Raw: strconv.FormatBool(val)}
	case json.Number:
		s := val.String()
		if strings.Contains(s, ".") {
			f, _ := val.Float64()
			return CellValue{FloatVal: f, Type: TypeFloat, Raw: s}
		}
		i, _ := val.Int64()
		return CellValue{IntVal: i, Type: TypeInt, Raw: s}
	default:
		s := fmt.Sprintf("%v", val)
		return CellValue{StrVal: s, Raw: s, Type: TypeString}
	}
}
