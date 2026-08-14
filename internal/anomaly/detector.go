package anomaly

import (
	"fmt"
	"math"
	"sort"

	"github.com/data-cleaner/internal/datasource"
)

type Anomaly struct {
	RowIndex int         `json:"row_index" yaml:"row_index"`
	Column   string      `json:"column" yaml:"column"`
	Method   string      `json:"method" yaml:"method"`
	Value    interface{} `json:"value" yaml:"value"`
	Reason   string      `json:"reason" yaml:"reason"`
}

func DetectIQR(ds *datasource.Dataset) []Anomaly {
	var anomalies []Anomaly

	for colIdx, col := range ds.Schema.Columns {
		if col.DataType != datasource.TypeInt && col.DataType != datasource.TypeFloat {
			continue
		}

		var values []float64
		rowIndices := []int{}
		for rowIdx, row := range ds.Rows {
			if colIdx >= len(row.Values) {
				continue
			}
			cell := row.Values[colIdx]
			if cell.IsNull {
				continue
			}
			v := CellToFloat(cell)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			values = append(values, v)
			rowIndices = append(rowIndices, rowIdx)
		}

		if len(values) < 4 {
			continue
		}

		sorted := make([]float64, len(values))
		copy(sorted, values)
		sort.Float64s(sorted)

		q1 := percentile(sorted, 25)
		q3 := percentile(sorted, 75)
		iqr := q3 - q1
		lowerBound := q1 - 3.0*iqr
		upperBound := q3 + 3.0*iqr

		for i, v := range values {
			if v < lowerBound {
				anomalies = append(anomalies, Anomaly{
					RowIndex: rowIndices[i],
					Column:   col.Name,
					Method:   "IQR",
					Value:    v,
					Reason:   fmt.Sprintf("value %v below lower bound %v (Q1-1.5*IQR)", v, lowerBound),
				})
			} else if v > upperBound {
				anomalies = append(anomalies, Anomaly{
					RowIndex: rowIndices[i],
					Column:   col.Name,
					Method:   "IQR",
					Value:    v,
					Reason:   fmt.Sprintf("value %v above upper bound %v (Q3+1.5*IQR)", v, upperBound),
				})
			}
		}
	}

	return anomalies
}

func DetectZScore(ds *datasource.Dataset) []Anomaly {
	var anomalies []Anomaly

	for colIdx, col := range ds.Schema.Columns {
		if col.DataType != datasource.TypeInt && col.DataType != datasource.TypeFloat {
			continue
		}

		var values []float64
		rowIndices := []int{}
		for rowIdx, row := range ds.Rows {
			if colIdx >= len(row.Values) {
				continue
			}
			cell := row.Values[colIdx]
			if cell.IsNull {
				continue
			}
			v := CellToFloat(cell)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			values = append(values, v)
			rowIndices = append(rowIndices, rowIdx)
		}

		if len(values) < 3 {
			continue
		}

		mean := calcMean(values)
		stdDev := calcStdDev(values, mean)

		if stdDev == 0 {
			continue
		}

		for i, v := range values {
			z := math.Abs(v-mean) / stdDev
			if z > 3 {
				anomalies = append(anomalies, Anomaly{
					RowIndex: rowIndices[i],
					Column:   col.Name,
					Method:   "Z-Score",
					Value:    v,
					Reason:   fmt.Sprintf("z-score %.2f > 3 (value=%v, mean=%.2f, std=%.2f)", z, v, mean, stdDev),
				})
			}
		}
	}

	return anomalies
}

func DetectFormatAnomalies(ds *datasource.Dataset) []Anomaly {
	var anomalies []Anomaly

	for colIdx, col := range ds.Schema.Columns {
		if col.DataType == datasource.TypeString || col.DataType == datasource.TypeUnknown {
			continue
		}

		for rowIdx, row := range ds.Rows {
			if colIdx >= len(row.Values) {
				continue
			}
			cell := row.Values[colIdx]
			if cell.IsNull || cell.Raw == "" {
				continue
			}

			detected := detectTypeFromRaw(cell.Raw)
			if detected != col.DataType && detected != datasource.TypeUnknown {
				anomalies = append(anomalies, Anomaly{
					RowIndex: rowIdx,
					Column:   col.Name,
					Method:   "Format",
					Value:    cell.Raw,
					Reason:   fmt.Sprintf("expected type %s but raw value '%s' appears to be %s", col.DataType, cell.Raw, detected),
				})
			}
		}
	}

	return anomalies
}

type BusinessRule struct {
	ID     string                 `yaml:"id" json:"id"`
	Type   string                 `yaml:"type" json:"type"`
	Params map[string]interface{} `yaml:"params" json:"params"`
}

func DetectBusinessLogic(ds *datasource.Dataset, rules []BusinessRule) []Anomaly {
	var anomalies []Anomaly

	for _, rule := range rules {
		switch rule.Type {
		case "cross_field_compare":
			fieldA, _ := rule.Params["field_a"].(string)
			fieldB, _ := rule.Params["field_b"].(string)
			op, _ := rule.Params["operator"].(string)

			idxA := ds.Schema.ColumnIndex(fieldA)
			idxB := ds.Schema.ColumnIndex(fieldB)
			if idxA < 0 || idxB < 0 {
				continue
			}

			for rowIdx, row := range ds.Rows {
				if idxA >= len(row.Values) || idxB >= len(row.Values) {
					continue
				}
				cellA := row.Values[idxA]
				cellB := row.Values[idxB]
				if cellA.IsNull || cellB.IsNull {
					continue
				}

				vA := CellToFloat(cellA)
				vB := CellToFloat(cellB)
				violated := false

				switch op {
				case "<=":
					violated = vA > vB
				case ">=":
					violated = vA < vB
				case "<":
					violated = vA >= vB
				case ">":
					violated = vA <= vB
				}

				if violated {
					anomalies = append(anomalies, Anomaly{
						RowIndex: rowIdx,
						Column:   fmt.Sprintf("%s,%s", fieldA, fieldB),
						Method:   "BusinessLogic",
						Value:    fmt.Sprintf("%v,%v", vA, vB),
						Reason:   fmt.Sprintf("%s(%v) %s %s(%v) violated", fieldA, vA, op, fieldB, vB),
					})
				}
			}
		}
	}

	return anomalies
}

func DetectAll(ds *datasource.Dataset, businessRules []BusinessRule) []Anomaly {
	var all []Anomaly
	all = append(all, DetectIQR(ds)...)
	all = append(all, DetectZScore(ds)...)
	all = append(all, DetectFormatAnomalies(ds)...)
	all = append(all, DetectBusinessLogic(ds, businessRules)...)
	return all
}

func CellToFloat(cell datasource.CellValue) float64 {
	switch cell.Type {
	case datasource.TypeInt:
		return float64(cell.IntVal)
	case datasource.TypeFloat:
		return cell.FloatVal
	case datasource.TypeBool:
		if cell.BoolVal {
			return 1
		}
		return 0
	case datasource.TypeDate:
		return float64(cell.DateVal.Unix())
	default:
		return math.NaN()
	}
}

func detectTypeFromRaw(s string) datasource.DataType {
	return datasource.TypeString
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

func calcStdDev(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)-1))
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
