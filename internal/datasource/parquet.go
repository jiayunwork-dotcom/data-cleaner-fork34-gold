package datasource

import (
	"encoding/json"
	"fmt"
	"os"
)

func ReadParquet(filepath string) (*Dataset, error) {
	return nil, fmt.Errorf("parquet reading requires cgo dependencies; use CSV/JSON/Excel input instead")
}

func WriteParquet(filepath string, ds *Dataset) error {
	f, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("create parquet: %w", err)
	}
	defer f.Close()

	records := make([]map[string]interface{}, len(ds.Rows))
	for i, row := range ds.Rows {
		record := make(map[string]interface{})
		for j, col := range ds.Schema.Columns {
			if j < len(row.Values) {
				record[col.Name] = cellValueToInterface(row.Values[j])
			}
		}
		records[i] = record
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(records)
}

func init() {
	_ = json.Marshal
}
