package incremental

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/data-cleaner/internal/datasource"
)

type Fingerprint struct {
	RowHashes    []string `json:"row_hashes"`
	RowCount     int      `json:"row_count"`
	ModTime      string   `json:"mod_time"`
	ConfigHash   string   `json:"config_hash"`
	Timestamp    string   `json:"timestamp"`
}

type DiffResult struct {
	AddedRows    []int `json:"added_rows"`
	DeletedRows  []int `json:"deleted_rows"`
	ModifiedRows []int `json:"modified_rows"`
	UnchangedRows []int `json:"unchanged_rows"`
	TotalBefore  int   `json:"total_before"`
	TotalAfter   int   `json:"total_after"`
}

type PreviousCleanRow struct {
	RowIndex int             `json:"row_index"`
	Values   []CellSnapshot  `json:"values"`
}

type CellSnapshot struct {
	Raw    string `json:"raw"`
	IsNull bool   `json:"is_null"`
	Type   string `json:"type"`
}

type IncrementalState struct {
	Fingerprint  Fingerprint       `json:"fingerprint"`
	CleanedRows  []PreviousCleanRow `json:"cleaned_rows"`
	Schema       SchemaSnapshot     `json:"schema"`
}

type SchemaSnapshot struct {
	Columns []ColumnSnapshot `json:"columns"`
}

type ColumnSnapshot struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Nullable bool   `json:"nullable"`
}

func ComputeFingerprint(ds *datasource.Dataset, configHash string) Fingerprint {
	hashes := make([]string, len(ds.Rows))
	modTime := time.Now().Format(time.RFC3339)

	for i, row := range ds.Rows {
		var sb strings.Builder
		for _, cell := range row.Values {
			sb.WriteString(datasource.FormatCellValue(cell))
			sb.WriteByte('|')
		}
		h := sha256.Sum256([]byte(sb.String()))
		hashes[i] = fmt.Sprintf("%x", h)
	}

	return Fingerprint{
		RowHashes:  hashes,
		RowCount:   len(ds.Rows),
		ModTime:    modTime,
		ConfigHash: configHash,
		Timestamp:  time.Now().Format(time.RFC3339),
	}
}

func DiffFingerprints(before, after Fingerprint) DiffResult {
	result := DiffResult{
		TotalBefore: before.RowCount,
		TotalAfter:  after.RowCount,
	}

	beforeMap := make(map[int]string)
	for i, h := range before.RowHashes {
		beforeMap[i] = h
	}

	afterMap := make(map[int]string)
	for i, h := range after.RowHashes {
		afterMap[i] = h
	}

	maxRows := before.RowCount
	if after.RowCount > maxRows {
		maxRows = after.RowCount
	}

	for i := 0; i < maxRows; i++ {
		beforeHash, beforeExists := beforeMap[i]
		afterHash, afterExists := afterMap[i]

		if beforeExists && afterExists {
			if beforeHash == afterHash {
				result.UnchangedRows = append(result.UnchangedRows, i)
			} else {
				result.ModifiedRows = append(result.ModifiedRows, i)
			}
		} else if !beforeExists && afterExists {
			result.AddedRows = append(result.AddedRows, i)
		} else if beforeExists && !afterExists {
			result.DeletedRows = append(result.DeletedRows, i)
		}
	}

	if after.RowCount > before.RowCount {
		for i := before.RowCount; i < after.RowCount; i++ {
			found := false
			for _, r := range result.AddedRows {
				if r == i {
					found = true
					break
				}
			}
			if !found {
				result.AddedRows = append(result.AddedRows, i)
			}
		}
	}

	return result
}

func SaveFingerprint(cacheDir string, ds *datasource.Dataset, configHash string, cleanedDS *datasource.Dataset) error {
	fp := ComputeFingerprint(ds, configHash)

	state := IncrementalState{
		Fingerprint: fp,
		CleanedRows: make([]PreviousCleanRow, len(cleanedDS.Rows)),
		Schema:      snapshotSchema(cleanedDS),
	}

	for i, row := range cleanedDS.Rows {
		snap := PreviousCleanRow{
			RowIndex: i,
			Values:   make([]CellSnapshot, len(row.Values)),
		}
		for j, cell := range row.Values {
			snap.Values[j] = CellSnapshot{
				Raw:    datasource.FormatCellValue(cell),
				IsNull: cell.IsNull,
				Type:   cell.Type.String(),
			}
		}
		state.CleanedRows[i] = snap
	}

	os.MkdirAll(cacheDir, 0755)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal incremental state: %w", err)
	}

	fpPath := filepath.Join(cacheDir, "incremental_state.json")
	if err := os.WriteFile(fpPath, data, 0644); err != nil {
		return fmt.Errorf("write incremental state: %w", err)
	}

	return nil
}

func LoadIncrementalState(cacheDir string) (*IncrementalState, error) {
	fpPath := filepath.Join(cacheDir, "incremental_state.json")
	data, err := os.ReadFile(fpPath)
	if err != nil {
		return nil, fmt.Errorf("read incremental state: %w", err)
	}

	var state IncrementalState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse incremental state: %w", err)
	}

	return &state, nil
}

func IsFingerprintValid(state *IncrementalState, currentConfigHash string) bool {
	if state == nil {
		return false
	}
	if state.Fingerprint.ConfigHash != currentConfigHash {
		return false
	}
	if len(state.CleanedRows) == 0 {
		return false
	}
	return true
}

func ReconstructCleanedRows(state *IncrementalState) *datasource.Dataset {
	if state == nil || len(state.CleanedRows) == 0 {
		return nil
	}

	ds := &datasource.Dataset{
		Name: "reconstructed",
		Schema: datasource.Schema{
			Columns: make([]datasource.ColumnSchema, len(state.Schema.Columns)),
		},
		Rows: make([]datasource.Row, len(state.CleanedRows)),
	}

	for i, col := range state.Schema.Columns {
		ds.Schema.Columns[i] = datasource.ColumnSchema{
			Name:     col.Name,
			DataType: datasource.DataTypeFromString(col.DataType),
			Nullable: col.Nullable,
		}
	}

	for i, prevRow := range state.CleanedRows {
		row := datasource.Row{Values: make([]datasource.CellValue, len(prevRow.Values))}
		for j, cellSnap := range prevRow.Values {
			cell := datasource.CellValue{
				Raw:    cellSnap.Raw,
				IsNull: cellSnap.IsNull,
				Type:   datasource.DataTypeFromString(cellSnap.Type),
			}
			if !cell.IsNull {
				switch cell.Type {
				case datasource.TypeInt:
					fmt.Sscanf(cellSnap.Raw, "%d", &cell.IntVal)
				case datasource.TypeFloat:
					fmt.Sscanf(cellSnap.Raw, "%f", &cell.FloatVal)
				case datasource.TypeBool:
					cell.BoolVal = cellSnap.Raw == "true"
				case datasource.TypeString:
					cell.StrVal = cellSnap.Raw
				case datasource.TypeDate:
					cell.StrVal = cellSnap.Raw
					for _, f := range []string{"2006-01-02", "2006/01/02", time.RFC3339} {
						if t, err := time.Parse(f, cellSnap.Raw); err == nil {
							cell.DateVal = t
							break
						}
					}
				}
			}
			row.Values[j] = cell
		}
		ds.Rows[i] = row
	}

	return ds
}

func snapshotSchema(ds *datasource.Dataset) SchemaSnapshot {
	cols := make([]ColumnSnapshot, len(ds.Schema.Columns))
	for i, col := range ds.Schema.Columns {
		cols[i] = ColumnSnapshot{
			Name:     col.Name,
			DataType: col.DataType.String(),
			Nullable: col.Nullable,
		}
	}
	return SchemaSnapshot{Columns: cols}
}
