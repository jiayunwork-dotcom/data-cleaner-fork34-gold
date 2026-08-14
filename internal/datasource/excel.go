package datasource

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

func ReadExcel(filepath string, sheetName string) (*Dataset, error) {
	f, err := excelize.OpenFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()

	if sheetName == "" {
		sheetName = f.GetSheetName(0)
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("read sheet %s: %w", sheetName, err)
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("empty sheet: %s", sheetName)
	}

	headers := rows[0]
	dataRows := rows[1:]

	ds := &Dataset{
		Name: fmt.Sprintf("%s:%s", filepath, sheetName),
		Schema: Schema{
			Columns: make([]ColumnSchema, len(headers)),
		},
		Rows: make([]Row, 0, len(dataRows)),
	}

	for i, h := range headers {
		ds.Schema.Columns[i] = ColumnSchema{
			Name:     strings.TrimSpace(h),
			DataType: TypeString,
			Nullable: true,
		}
	}

	for _, record := range dataRows {
		row := Row{Values: make([]CellValue, len(headers))}
		for i := range headers {
			if i < len(record) {
				row.Values[i] = parseCellValue(strings.TrimSpace(record[i]))
			} else {
				row.Values[i] = CellValue{IsNull: true, Type: TypeUnknown}
			}
		}
		ds.Rows = append(ds.Rows, row)
	}

	inferSchema(ds)
	return ds, nil
}

func ListExcelSheets(filepath string) ([]string, error) {
	f, err := excelize.OpenFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()
	return f.GetSheetList(), nil
}

func ReadExcelAllSheets(filepath string) (map[string]*Dataset, error) {
	sheets, err := ListExcelSheets(filepath)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*Dataset)
	for _, s := range sheets {
		ds, err := ReadExcel(filepath, s)
		if err != nil {
			return nil, fmt.Errorf("read sheet %s: %w", s, err)
		}
		result[s] = ds
	}
	return result, nil
}

func parseCellValue(s string) CellValue {
	if s == "" {
		return CellValue{IsNull: true, Type: TypeUnknown}
	}
	if s == "true" || s == "TRUE" || s == "True" {
		return CellValue{BoolVal: true, Type: TypeBool, Raw: s}
	}
	if s == "false" || s == "FALSE" || s == "False" {
		return CellValue{BoolVal: false, Type: TypeBool, Raw: s}
	}
	if iv, err := strconv.ParseInt(s, 10, 64); err == nil {
		return CellValue{IntVal: iv, Type: TypeInt, Raw: s}
	}
	if fv, err := strconv.ParseFloat(s, 64); err == nil {
		return CellValue{FloatVal: fv, Type: TypeFloat, Raw: s}
	}
	return CellValue{StrVal: s, Type: TypeString, Raw: s}
}
