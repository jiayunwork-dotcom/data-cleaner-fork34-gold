package cleaning

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/data-cleaner/internal/anomaly"
	"github.com/data-cleaner/internal/audit"
	"github.com/data-cleaner/internal/datasource"
)

type MissingStrategy string

const (
	MissingMean      MissingStrategy = "mean"
	MissingMedian    MissingStrategy = "median"
	MissingMode      MissingStrategy = "mode"
	MissingKNN       MissingStrategy = "knn"
	MissingForward   MissingStrategy = "forward_fill"
	MissingFixed     MissingStrategy = "fixed_value"
	MissingDropRow   MissingStrategy = "drop_row"
)

type OutlierStrategy string

const (
	OutlierWinsorize OutlierStrategy = "winsorize"
	OutlierToNull    OutlierStrategy = "to_null"
	OutlierDropRow   OutlierStrategy = "drop_row"
)

type FormatStrategy string

const (
	FormatDateISO     FormatStrategy = "date_iso8601"
	FormatPhone       FormatStrategy = "phone_standardize"
	FormatAddress     FormatStrategy = "address_split"
	FormatUppercase   FormatStrategy = "uppercase"
	FormatLowercase   FormatStrategy = "lowercase"
	FormatTitleCase   FormatStrategy = "titlecase"
	FormatTrim        FormatStrategy = "trim"
)

type DedupKeepStrategy string

const (
	DedupKeepFirst   DedupKeepStrategy = "first"
	DedupKeepLast    DedupKeepStrategy = "last"
	DedupKeepLatest  DedupKeepStrategy = "latest_timestamp"
)

type MissingConfig struct {
	Columns   map[string]MissingStrategy `yaml:"columns" json:"columns"`
	FixedValue map[string]string         `yaml:"fixed_values" json:"fixed_values"`
	KNNK      int                        `yaml:"knn_k" json:"knn_k"`
}

type OutlierConfig struct {
	Strategy OutlierStrategy `yaml:"strategy" json:"strategy"`
	Columns  []string        `yaml:"columns" json:"columns"`
}

type FormatConfig struct {
	Rules []FormatRule `yaml:"rules" json:"rules"`
}

type FormatRule struct {
	Column   string         `yaml:"column" yaml:"column"`
	Strategy FormatStrategy `yaml:"strategy" json:"strategy"`
	Params   map[string]string `yaml:"params" json:"params"`
}

type DedupConfig struct {
	Columns    []string          `yaml:"columns" json:"columns"`
	Keep       DedupKeepStrategy `yaml:"keep" json:"keep"`
	Timestamp  string            `yaml:"timestamp_column" json:"timestamp_column"`
}

type Cleaner struct {
	MissingCfg  *MissingConfig
	OutlierCfg  *OutlierConfig
	FormatCfg   *FormatConfig
	DedupCfg    *DedupConfig
	AuditLogger *audit.Logger
	DryRun      bool
}

func NewCleaner(missing *MissingConfig, outlier *OutlierConfig, format *FormatConfig, dedup *DedupConfig, auditLog *audit.Logger, dryRun bool) *Cleaner {
	return &Cleaner{
		MissingCfg:  missing,
		OutlierCfg:  outlier,
		FormatCfg:   format,
		DedupCfg:    dedup,
		AuditLogger: auditLog,
		DryRun:      dryRun,
	}
}

func (c *Cleaner) Clean(ds *datasource.Dataset) *datasource.Dataset {
	result := copyDataset(ds)

	result = c.HandleMissing(result)
	result = c.HandleOutliers(result)
	result = c.StandardizeFormats(result)
	result = c.Deduplicate(result)

	return result
}

func (c *Cleaner) HandleMissing(ds *datasource.Dataset) *datasource.Dataset {
	if c.MissingCfg == nil {
		return ds
	}

	dropRows := make(map[int]bool)

	for colName, strategy := range c.MissingCfg.Columns {
		colIdx := ds.Schema.ColumnIndex(colName)
		if colIdx < 0 {
			continue
		}

		switch strategy {
		case MissingMean:
			c.fillMean(ds, colIdx, colName)
		case MissingMedian:
			c.fillMedian(ds, colIdx, colName)
		case MissingMode:
			c.fillMode(ds, colIdx, colName)
		case MissingKNN:
			c.fillKNN(ds, colIdx, colName)
		case MissingForward:
			c.fillForward(ds, colIdx, colName)
		case MissingFixed:
			c.fillFixed(ds, colIdx, colName)
		case MissingDropRow:
			for rowIdx, row := range ds.Rows {
				if colIdx < len(row.Values) && row.Values[colIdx].IsNull {
					dropRows[rowIdx] = true
				}
			}
		}
	}

	if len(dropRows) > 0 {
		var newRows []datasource.Row
		for rowIdx, row := range ds.Rows {
			if !dropRows[rowIdx] {
				newRows = append(newRows, row)
			}
		}
		ds.Rows = newRows
	}

	return ds
}

func (c *Cleaner) fillMean(ds *datasource.Dataset, colIdx int, colName string) {
	var values []float64
	for _, row := range ds.Rows {
		if colIdx < len(row.Values) && !row.Values[colIdx].IsNull {
			v := anomaly.CellToFloat(row.Values[colIdx])
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				values = append(values, v)
			}
		}
	}
	if len(values) == 0 {
		return
	}
	mean := calcMean(values)

	for rowIdx, row := range ds.Rows {
		if colIdx < len(row.Values) && row.Values[colIdx].IsNull {
			oldVal := ds.Rows[rowIdx].Values[colIdx]
			newCell := datasource.CellValue{
				FloatVal: mean,
				Type:     datasource.TypeFloat,
				Raw:      fmt.Sprintf("%.6f", mean),
			}
			c.logChange(ds.Name, rowIdx, colName, oldVal, newCell, "mean_fill")
			ds.Rows[rowIdx].Values[colIdx] = newCell
		}
	}
}

func getCellTime(cell datasource.CellValue) (time.Time, bool) {
	if !cell.DateVal.IsZero() {
		return cell.DateVal, true
	}
	if cell.Type == datasource.TypeDate {
		t, err := time.Parse("2006-01-02", cell.Raw)
		if err == nil {
			return t, true
		}
	}
	if cell.Type == datasource.TypeInt || cell.Type == datasource.TypeFloat {
		var ts int64
		if cell.Type == datasource.TypeInt {
			ts = cell.IntVal
		} else {
			ts = int64(cell.FloatVal)
		}
		if ts > 0 {
			return time.Unix(ts, 0), true
		}
	}
	return time.Time{}, false
}

func (c *Cleaner) fillMedian(ds *datasource.Dataset, colIdx int, colName string) {
	col := ds.Schema.Columns[colIdx]
	isDate := col.DataType == datasource.TypeDate

	var values []float64
	var dateValues []time.Time
	for _, row := range ds.Rows {
		if colIdx < len(row.Values) && !row.Values[colIdx].IsNull {
			if isDate {
				if t, ok := getCellTime(row.Values[colIdx]); ok {
					dateValues = append(dateValues, t)
				}
			} else {
				v := anomaly.CellToFloat(row.Values[colIdx])
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					values = append(values, v)
				}
			}
		}
	}

	var medianTime time.Time
	var median float64
	hasData := false

	if isDate {
		if len(dateValues) == 0 {
			return
		}
		sort.Slice(dateValues, func(i, j int) bool {
			return dateValues[i].Before(dateValues[j])
		})
		medianTime = dateValues[len(dateValues)/2]
		hasData = true
	} else {
		if len(values) == 0 {
			return
		}
		sort.Float64s(values)
		median = values[len(values)/2]
		if len(values)%2 == 0 {
			median = (values[len(values)/2-1] + values[len(values)/2]) / 2
		}
		hasData = true
	}

	if !hasData {
		return
	}

	for rowIdx, row := range ds.Rows {
		if colIdx < len(row.Values) && row.Values[colIdx].IsNull {
			oldVal := ds.Rows[rowIdx].Values[colIdx]
			var newCell datasource.CellValue
			if isDate {
				newCell = datasource.CellValue{
					DateVal:  medianTime,
					Type:     datasource.TypeDate,
					Raw:      medianTime.Format("2006-01-02"),
				}
			} else {
				newCell = datasource.CellValue{
					FloatVal: median,
					Type:     datasource.TypeFloat,
					Raw:      fmt.Sprintf("%.6f", median),
				}
			}
			c.logChange(ds.Name, rowIdx, colName, oldVal, newCell, "median_fill")
			ds.Rows[rowIdx].Values[colIdx] = newCell
		}
	}
}

func (c *Cleaner) fillMode(ds *datasource.Dataset, colIdx int, colName string) {
	counts := make(map[string]int)
	for _, row := range ds.Rows {
		if colIdx < len(row.Values) && !row.Values[colIdx].IsNull {
			val := datasource.FormatCellValue(row.Values[colIdx])
			counts[val]++
		}
	}
	if len(counts) == 0 {
		return
	}
	mode := ""
	maxCount := 0
	for val, count := range counts {
		if count > maxCount {
			maxCount = count
			mode = val
		}
	}

	for rowIdx, row := range ds.Rows {
		if colIdx < len(row.Values) && row.Values[colIdx].IsNull {
			oldVal := ds.Rows[rowIdx].Values[colIdx]
			newCell := datasource.CellValue{StrVal: mode, Raw: mode, Type: datasource.TypeString}
			col := ds.Schema.Columns[colIdx]
			if col.DataType == datasource.TypeInt {
				if iv, err := strconv.ParseInt(mode, 10, 64); err == nil {
					newCell = datasource.CellValue{IntVal: iv, Raw: mode, Type: datasource.TypeInt}
				}
			} else if col.DataType == datasource.TypeFloat {
				if fv, err := strconv.ParseFloat(mode, 64); err == nil {
					newCell = datasource.CellValue{FloatVal: fv, Raw: mode, Type: datasource.TypeFloat}
				}
			}
			c.logChange(ds.Name, rowIdx, colName, oldVal, newCell, "mode_fill")
			ds.Rows[rowIdx].Values[colIdx] = newCell
		}
	}
}

func (c *Cleaner) fillKNN(ds *datasource.Dataset, colIdx int, colName string) {
	k := 5
	if c.MissingCfg.KNNK > 0 {
		k = c.MissingCfg.KNNK
	}
	if len(ds.Rows) < 100 && k > 3 {
		k = 3
	}

	var completeRows []int
	var incompleteRows []int

	for rowIdx, row := range ds.Rows {
		if colIdx < len(row.Values) {
			if row.Values[colIdx].IsNull {
				incompleteRows = append(incompleteRows, rowIdx)
			} else {
				completeRows = append(completeRows, rowIdx)
			}
		}
	}

	if len(completeRows) < k {
		c.fillMean(ds, colIdx, colName)
		return
	}

	numCols := []int{}
	for i, col := range ds.Schema.Columns {
		if (col.DataType == datasource.TypeInt || col.DataType == datasource.TypeFloat) && i != colIdx {
			numCols = append(numCols, i)
		}
	}

	for _, missIdx := range incompleteRows {
		neighbors := findKNearest(ds, missIdx, completeRows, numCols, k)
		if len(neighbors) == 0 {
			continue
		}

		weightedSum := 0.0
		weightTotal := 0.0
		for _, nIdx := range neighbors {
			dist := euclideanDistance(ds, missIdx, nIdx, numCols)
			weight := 1.0
			if dist > 0 {
				weight = 1.0 / dist
			}
			val := anomaly.CellToFloat(ds.Rows[nIdx].Values[colIdx])
			if !math.IsNaN(val) {
				weightedSum += val * weight
				weightTotal += weight
			}
		}

		if weightTotal > 0 {
			fillVal := weightedSum / weightTotal
			oldVal := ds.Rows[missIdx].Values[colIdx]
			newCell := datasource.CellValue{
				FloatVal: fillVal,
				Type:     datasource.TypeFloat,
				Raw:      fmt.Sprintf("%.6f", fillVal),
			}
			c.logChange(ds.Name, missIdx, colName, oldVal, newCell, "knn_fill")
			ds.Rows[missIdx].Values[colIdx] = newCell
		}
	}
}

func (c *Cleaner) fillForward(ds *datasource.Dataset, colIdx int, colName string) {
	var lastVal *datasource.CellValue
	for rowIdx, row := range ds.Rows {
		if colIdx >= len(row.Values) {
			continue
		}
		if row.Values[colIdx].IsNull {
			if lastVal != nil {
				oldVal := ds.Rows[rowIdx].Values[colIdx]
				c.logChange(ds.Name, rowIdx, colName, oldVal, *lastVal, "forward_fill")
				ds.Rows[rowIdx].Values[colIdx] = *lastVal
			}
		} else {
			copy := row.Values[colIdx]
			lastVal = &copy
		}
	}
}

func (c *Cleaner) fillFixed(ds *datasource.Dataset, colIdx int, colName string) {
	fixedVal, ok := c.MissingCfg.FixedValue[colName]
	if !ok {
		return
	}

	for rowIdx, row := range ds.Rows {
		if colIdx < len(row.Values) && row.Values[colIdx].IsNull {
			oldVal := ds.Rows[rowIdx].Values[colIdx]
			newCell := datasource.CellValue{StrVal: fixedVal, Raw: fixedVal, Type: datasource.TypeString}
			col := ds.Schema.Columns[colIdx]
			if col.DataType == datasource.TypeInt {
				if iv, err := strconv.ParseInt(fixedVal, 10, 64); err == nil {
					newCell = datasource.CellValue{IntVal: iv, Raw: fixedVal, Type: datasource.TypeInt}
				}
			} else if col.DataType == datasource.TypeFloat {
				if fv, err := strconv.ParseFloat(fixedVal, 64); err == nil {
					newCell = datasource.CellValue{FloatVal: fv, Raw: fixedVal, Type: datasource.TypeFloat}
				}
			}
			c.logChange(ds.Name, rowIdx, colName, oldVal, newCell, "fixed_fill")
			ds.Rows[rowIdx].Values[colIdx] = newCell
		}
	}
}

func (c *Cleaner) HandleOutliers(ds *datasource.Dataset) *datasource.Dataset {
	if c.OutlierCfg == nil {
		return ds
	}

	anomalies := anomaly.DetectIQR(ds)
	dropRows := make(map[int]bool)

	for _, a := range anomalies {
		matched := false
		if len(c.OutlierCfg.Columns) == 0 {
			matched = true
		} else {
			for _, col := range c.OutlierCfg.Columns {
				if col == a.Column {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}

		colIdx := ds.Schema.ColumnIndex(a.Column)
		if colIdx < 0 {
			continue
		}

		switch c.OutlierCfg.Strategy {
		case OutlierWinsorize:
			c.winsorize(ds, colIdx, a.Column)
		case OutlierToNull:
			if a.RowIndex < len(ds.Rows) && colIdx < len(ds.Rows[a.RowIndex].Values) {
				oldVal := ds.Rows[a.RowIndex].Values[colIdx]
				newCell := datasource.CellValue{IsNull: true, Type: ds.Schema.Columns[colIdx].DataType}
				c.logChange(ds.Name, a.RowIndex, a.Column, oldVal, newCell, "outlier_to_null")
				ds.Rows[a.RowIndex].Values[colIdx] = newCell
			}
		case OutlierDropRow:
			dropRows[a.RowIndex] = true
		}
	}

	if len(dropRows) > 0 {
		var newRows []datasource.Row
		for rowIdx, row := range ds.Rows {
			if !dropRows[rowIdx] {
				newRows = append(newRows, row)
			}
		}
		ds.Rows = newRows
	}

	return ds
}

func (c *Cleaner) winsorize(ds *datasource.Dataset, colIdx int, colName string) {
	var values []float64
	for _, row := range ds.Rows {
		if colIdx < len(row.Values) && !row.Values[colIdx].IsNull {
			v := anomaly.CellToFloat(row.Values[colIdx])
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				values = append(values, v)
			}
		}
	}
	if len(values) < 4 {
		return
	}

	sort.Float64s(values)
	q1 := percentile(values, 25)
	q3 := percentile(values, 75)
	iqr := q3 - q1
	lowerBound := q1 - 1.5*iqr
	upperBound := q3 + 1.5*iqr

	for rowIdx, row := range ds.Rows {
		if colIdx >= len(row.Values) || row.Values[colIdx].IsNull {
			continue
		}
		v := anomaly.CellToFloat(row.Values[colIdx])
		if math.IsNaN(v) {
			continue
		}

		if v < lowerBound {
			oldVal := ds.Rows[rowIdx].Values[colIdx]
			newCell := datasource.CellValue{FloatVal: lowerBound, Type: datasource.TypeFloat, Raw: fmt.Sprintf("%.6f", lowerBound)}
			c.logChange(ds.Name, rowIdx, colName, oldVal, newCell, "winsorize_lower")
			ds.Rows[rowIdx].Values[colIdx] = newCell
		} else if v > upperBound {
			oldVal := ds.Rows[rowIdx].Values[colIdx]
			newCell := datasource.CellValue{FloatVal: upperBound, Type: datasource.TypeFloat, Raw: fmt.Sprintf("%.6f", upperBound)}
			c.logChange(ds.Name, rowIdx, colName, oldVal, newCell, "winsorize_upper")
			ds.Rows[rowIdx].Values[colIdx] = newCell
		}
	}
}

func (c *Cleaner) StandardizeFormats(ds *datasource.Dataset) *datasource.Dataset {
	if c.FormatCfg == nil {
		return ds
	}

	for _, rule := range c.FormatCfg.Rules {
		colIdx := ds.Schema.ColumnIndex(rule.Column)
		if colIdx < 0 {
			continue
		}

		for rowIdx, row := range ds.Rows {
			if colIdx >= len(row.Values) {
				continue
			}
			cell := row.Values[colIdx]
			if cell.IsNull {
				continue
			}

			var newCell datasource.CellValue
			changed := false

			switch rule.Strategy {
			case FormatDateISO:
				newCell, changed = c.standardizeDate(cell, rule.Params)
			case FormatPhone:
				newCell, changed = c.standardizePhone(cell)
			case FormatUppercase:
				if cell.Type == datasource.TypeString {
					upper := strings.ToUpper(cell.StrVal)
					if upper != cell.StrVal {
						newCell = datasource.CellValue{StrVal: upper, Raw: upper, Type: datasource.TypeString}
						changed = true
					}
				}
			case FormatLowercase:
				if cell.Type == datasource.TypeString {
					lower := strings.ToLower(cell.StrVal)
					if lower != cell.StrVal {
						newCell = datasource.CellValue{StrVal: lower, Raw: lower, Type: datasource.TypeString}
						changed = true
					}
				}
			case FormatTitleCase:
				if cell.Type == datasource.TypeString {
					title := toTitleCase(cell.StrVal)
					if title != cell.StrVal {
						newCell = datasource.CellValue{StrVal: title, Raw: title, Type: datasource.TypeString}
						changed = true
					}
				}
			case FormatTrim:
				if cell.Type == datasource.TypeString {
					trimmed := strings.TrimLeft(cell.StrVal, " \t")
					if trimmed != cell.StrVal {
						newCell = datasource.CellValue{StrVal: trimmed, Raw: trimmed, Type: datasource.TypeString}
						changed = true
					}
				}
			case FormatAddress:
				newCell, changed = c.standardizeAddress(cell)
			}

			if changed {
				c.logChange(ds.Name, rowIdx, rule.Column, cell, newCell, string(rule.Strategy))
				ds.Rows[rowIdx].Values[colIdx] = newCell
			}
		}
	}

	return ds
}

func (c *Cleaner) standardizeDate(cell datasource.CellValue, params map[string]string) (datasource.CellValue, bool) {
	if cell.Type != datasource.TypeDate && cell.Type != datasource.TypeString {
		return cell, false
	}

	var t time.Time
	var err error
	if cell.Type == datasource.TypeDate {
		t = cell.DateVal
	} else {
		formats := []string{
			"2006-01-02", "2006/01/02", "01/02/2006", "01-02-2006",
			"2006-01-02T15:04:05Z", "2006-01-02 15:04:05",
			time.RFC3339,
		}
		for _, fmt := range formats {
			t, err = time.Parse(fmt, cell.StrVal)
			if err == nil {
				break
			}
		}
		if err != nil {
			return cell, false
		}
	}

	isoFormat := "2006-01-02"
	if params != nil {
		if f, ok := params["format"]; ok {
			isoFormat = f
		}
	}

	newStr := t.Format(isoFormat)
	if newStr != cell.Raw {
		return datasource.CellValue{DateVal: t, StrVal: newStr, Raw: newStr, Type: datasource.TypeDate}, true
	}
	return cell, false
}

func (c *Cleaner) standardizePhone(cell datasource.CellValue) (datasource.CellValue, bool) {
	if cell.Type != datasource.TypeString {
		return cell, false
	}
	phone := cell.StrVal
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	phone = strings.ReplaceAll(phone, ".", "")

	if len(phone) > 0 && phone[0] != '+' {
		phone = "+86" + phone
	}

	if phone != cell.StrVal {
		return datasource.CellValue{StrVal: phone, Raw: phone, Type: datasource.TypeString}, true
	}
	return cell, false
}

func (c *Cleaner) standardizeAddress(cell datasource.CellValue) (datasource.CellValue, bool) {
	if cell.Type != datasource.TypeString {
		return cell, false
	}
	return cell, false
}

func (c *Cleaner) Deduplicate(ds *datasource.Dataset) *datasource.Dataset {
	if c.DedupCfg == nil || len(c.DedupCfg.Columns) == 0 {
		return ds
	}

	keyIndices := make([]int, 0, len(c.DedupCfg.Columns))
	for _, col := range c.DedupCfg.Columns {
		idx := ds.Schema.ColumnIndex(col)
		if idx >= 0 {
			keyIndices = append(keyIndices, idx)
		}
	}

	if len(keyIndices) == 0 {
		return ds
	}

	seen := make(map[string][]int)
	for rowIdx, row := range ds.Rows {
		key := makeKeyStr(row, keyIndices)
		seen[key] = append(seen[key], rowIdx)
	}

	dropSet := make(map[int]bool)
	for _, indices := range seen {
		if len(indices) <= 1 {
			continue
		}

		keepIdx := 0
		switch c.DedupCfg.Keep {
		case DedupKeepFirst:
			keepIdx = 0
		case DedupKeepLast:
			keepIdx = len(indices) - 1
		case DedupKeepLatest:
			tsCol := c.DedupCfg.Timestamp
			if tsCol != "" {
				tsIdx := ds.Schema.ColumnIndex(tsCol)
				if tsIdx >= 0 {
					latestTime := time.Time{}
					latestIdx := 0
					for i, rowIdx := range indices {
						if tsIdx < len(ds.Rows[rowIdx].Values) {
							cell := ds.Rows[rowIdx].Values[tsIdx]
							if !cell.IsNull && cell.Type == datasource.TypeDate && cell.DateVal.After(latestTime) {
								latestTime = cell.DateVal
								latestIdx = i
							}
						}
					}
					keepIdx = latestIdx
				}
			}
		}

		for i, rowIdx := range indices {
			if i != keepIdx {
				dropSet[rowIdx] = true
			}
		}
	}

	if len(dropSet) > 0 {
		var newRows []datasource.Row
		for rowIdx, row := range ds.Rows {
			if !dropSet[rowIdx] {
				newRows = append(newRows, row)
			}
		}
		ds.Rows = newRows
	}

	return ds
}

func (c *Cleaner) logChange(dataset string, rowIdx int, colName string, oldVal, newVal datasource.CellValue, strategy string) {
	if c.AuditLogger != nil {
		c.AuditLogger.Log(audit.AuditEntry{
			Dataset:  dataset,
			RowIdx:   rowIdx,
			Column:   colName,
			OldValue: datasource.FormatCellValue(oldVal),
			NewValue: datasource.FormatCellValue(newVal),
			Rule:     strategy,
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}

func copyDataset(ds *datasource.Dataset) *datasource.Dataset {
	result := &datasource.Dataset{
		Name:   ds.Name,
		Schema: ds.Schema,
		Rows:   make([]datasource.Row, len(ds.Rows)),
	}
	copy(result.Schema.Columns, ds.Schema.Columns)
	for i, row := range ds.Rows {
		result.Rows[i] = datasource.Row{Values: make([]datasource.CellValue, len(row.Values))}
		copy(result.Rows[i].Values, row.Values)
	}
	return result
}

func makeKeyStr(row datasource.Row, indices []int) string {
	parts := make([]string, len(indices))
	for i, idx := range indices {
		if idx < len(row.Values) {
			parts[i] = datasource.FormatCellValue(row.Values[idx])
		} else {
			parts[i] = "NULL"
		}
	}
	return strings.Join(parts, "|")
}

func toTitleCase(s string) string {
	return strings.Title(strings.ToLower(s))
}

func calcMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func findKNearest(ds *datasource.Dataset, targetIdx int, candidates []int, numCols []int, k int) []int {
	type distEntry struct {
		idx  int
		dist float64
	}

	var entries []distEntry
	for _, cIdx := range candidates {
		d := euclideanDistance(ds, targetIdx, cIdx, numCols)
		entries = append(entries, distEntry{idx: cIdx, dist: d})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].dist < entries[j].dist
	})

	if k > len(entries) {
		k = len(entries)
	}

	result := make([]int, k)
	for i := 0; i < k; i++ {
		result[i] = entries[i].idx
	}
	return result
}

func euclideanDistance(ds *datasource.Dataset, rowA, rowB int, numCols []int) float64 {
	sum := 0.0
	for _, colIdx := range numCols {
		if colIdx >= len(ds.Rows[rowA].Values) || colIdx >= len(ds.Rows[rowB].Values) {
			continue
		}
		a := anomaly.CellToFloat(ds.Rows[rowA].Values[colIdx])
		b := anomaly.CellToFloat(ds.Rows[rowB].Values[colIdx])
		if math.IsNaN(a) || math.IsNaN(b) {
			continue
		}
		d := a - b
		sum += d * d
	}
	return math.Sqrt(sum)
}
