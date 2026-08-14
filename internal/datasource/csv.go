package datasource

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

func detectDelimiter(header string) rune {
	counts := map[rune]int{',': 0, '\t': 0, ';': 0, '|': 0}
	for _, r := range header {
		if _, ok := counts[r]; ok {
			counts[r]++
		}
	}
	maxR := ','
	maxC := 0
	for r, c := range counts {
		if c > maxC {
			maxC = c
			maxR = r
		}
	}
	return rune(maxR)
}

func detectEncoding(data []byte) string {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return "utf8-bom"
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return "utf16le"
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return "utf16be"
		}
	}
	if utf8.Valid(data) {
		return "utf8"
	}
	return "utf8"
}

func ReadCSV(filepath string) (*Dataset, error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("open csv: %w", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}

	_ = detectEncoding(raw)

	content := string(raw)
	if len(content) > 0 && content[0] == '\xEF' {
		content = strings.TrimPrefix(content, "\xEF\xBB\xBF")
	}

	reader := csv.NewReader(strings.NewReader(content))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("empty csv file")
	}

	headers := records[0]
	dataRecords := records[1:]

	ds := &Dataset{
		Name: filepath,
		Schema: Schema{
			Columns: make([]ColumnSchema, len(headers)),
		},
		Rows: make([]Row, 0, len(dataRecords)),
	}

	for i, h := range headers {
		ds.Schema.Columns[i] = ColumnSchema{
			Name:     strings.TrimSpace(h),
			DataType: TypeString,
			Nullable: true,
		}
	}

	for _, record := range dataRecords {
		row := Row{Values: make([]CellValue, len(headers))}
		for i, val := range record {
			if i >= len(headers) {
				continue
			}
			row.Values[i] = parseCellValue(strings.TrimSpace(val))
		}
		ds.Rows = append(ds.Rows, row)
	}

	inferSchema(ds)
	return ds, nil
}
