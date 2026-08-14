package recommend

import (
	"math"
	"sort"

	"github.com/data-cleaner/internal/datasource"
)

func AnalyzeColumn(rows []datasource.Row, colIdx int, colName string, dataType datasource.DataType, isSampled bool) *ColumnStats {
	stats := &ColumnStats{
		ColumnName:       colName,
		DataType:         dataType,
		TotalRows:        len(rows),
		TopValues:        []ValueCount{},
		TypeDistribution: make(map[datasource.DataType]int),
		IsSampled:        isSampled,
	}

	valueCounts := make(map[string]int)
	numericValues := []float64{}
	stringLengths := []int{}
	nullCount := 0

	for _, row := range rows {
		if colIdx >= len(row.Values) {
			nullCount++
			stats.TypeDistribution[datasource.TypeUnknown]++
			continue
		}

		cell := row.Values[colIdx]
		stats.TypeDistribution[cell.Type]++

		if cell.IsNull {
			nullCount++
			continue
		}

		valStr := datasource.FormatCellValue(cell)
		valueCounts[valStr]++

		if cell.Type == datasource.TypeInt || cell.Type == datasource.TypeFloat {
			numericValues = append(numericValues, cellToFloat(cell))
		}

		if cell.Type == datasource.TypeString {
			stringLengths = append(stringLengths, len(valStr))
		}
	}

	stats.NullCount = nullCount
	if stats.TotalRows > 0 {
		stats.NullRate = float64(nullCount) / float64(stats.TotalRows)
	}

	stats.UniqueCount = len(valueCounts)
	if stats.TotalRows > 0 {
		stats.UniqueRate = float64(stats.UniqueCount) / float64(stats.TotalRows)
	}

	if len(numericValues) > 0 {
		stats.NumericStats = calculateNumericStats(numericValues)
	}

	if len(stringLengths) > 0 {
		stats.StringStats = calculateStringStats(stringLengths)
	}

	stats.TopValues = getTopValues(valueCounts, 5)

	return stats
}

func AnalyzeColumnFullScan(ds *datasource.Dataset, colIdx int, colName string, dataType datasource.DataType) *ColumnStats {
	return AnalyzeColumn(ds.Rows, colIdx, colName, dataType, false)
}

func calculateNumericStats(values []float64) *NumericStats {
	if len(values) == 0 {
		return nil
	}

	min := values[0]
	max := values[0]
	sum := 0.0

	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}

	mean := sum / float64(len(values))

	varianceSum := 0.0
	for _, v := range values {
		diff := v - mean
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(len(values)))

	return &NumericStats{
		Min:    min,
		Max:    max,
		Mean:   mean,
		StdDev: stdDev,
	}
}

func calculateStringStats(lengths []int) *StringStats {
	if len(lengths) == 0 {
		return nil
	}

	minLen := lengths[0]
	maxLen := lengths[0]
	sum := 0
	lengthCounts := make(map[int]int)

	for _, l := range lengths {
		if l < minLen {
			minLen = l
		}
		if l > maxLen {
			maxLen = l
		}
		sum += l
		lengthCounts[l]++
	}

	return &StringStats{
		MinLength:    minLen,
		MaxLength:    maxLen,
		AvgLength:    float64(sum) / float64(len(lengths)),
		LengthCounts: lengthCounts,
	}
}

func getTopValues(valueCounts map[string]int, topN int) []ValueCount {
	type kv struct {
		Key   string
		Value int
	}

	var sorted []kv
	for k, v := range valueCounts {
		sorted = append(sorted, kv{k, v})
	}

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Value > sorted[j].Value
	})

	result := make([]ValueCount, 0, topN)
	for i := 0; i < topN && i < len(sorted); i++ {
		result = append(result, ValueCount{
			Value: sorted[i].Key,
			Count: sorted[i].Value,
		})
	}

	return result
}

func cellToFloat(cell datasource.CellValue) float64 {
	switch cell.Type {
	case datasource.TypeInt:
		return float64(cell.IntVal)
	case datasource.TypeFloat:
		return cell.FloatVal
	case datasource.TypeDate:
		return float64(cell.DateVal.Unix())
	case datasource.TypeBool:
		if cell.BoolVal {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func GetColumnValues(rows []datasource.Row, colIdx int) map[string]bool {
	values := make(map[string]bool)
	for _, row := range rows {
		if colIdx >= len(row.Values) {
			continue
		}
		cell := row.Values[colIdx]
		if cell.IsNull {
			continue
		}
		values[datasource.FormatCellValue(cell)] = true
	}
	return values
}
