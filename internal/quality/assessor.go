package quality

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/data-cleaner/internal/datasource"
)

type DimensionScore struct {
	Dimension string  `json:"dimension" yaml:"dimension"`
	Score     float64 `json:"score" yaml:"score"`
	Details   string  `json:"details" yaml:"details"`
}

type QualityReport struct {
	DatasetName    string          `json:"dataset_name" yaml:"dataset_name"`
	Timestamp      string          `json:"timestamp" yaml:"timestamp"`
	TotalRows      int             `json:"total_rows" yaml:"total_rows"`
	TotalColumns   int             `json:"total_columns" yaml:"total_columns"`
	Dimensions     []DimensionScore `json:"dimensions" yaml:"dimensions"`
	DQI            float64         `json:"dqi" yaml:"dqi"`
	RowQuality     []RowQuality    `json:"row_quality" yaml:"row_quality"`
	ColumnScores   []ColumnScore   `json:"column_scores" yaml:"column_scores"`
}

type RowQuality struct {
	RowIndex int    `json:"row_index" yaml:"row_index"`
	Status   string `json:"status" yaml:"status"`
	Issues   []string `json:"issues" yaml:"issues"`
}

type ColumnScore struct {
	ColumnName   string  `json:"column_name" yaml:"column_name"`
	Completeness float64 `json:"completeness" yaml:"completeness"`
	Validity     float64 `json:"validity" yaml:"validity"`
}

type QualityConfig struct {
	Weights         map[string]float64 `yaml:"weights" json:"weights"`
	TimelinessThreshold *string        `yaml:"timeliness_threshold" json:"timeliness_threshold"`
	ConsistencyRules   []ConsistencyRule `yaml:"consistency_rules" json:"consistency_rules"`
	ValidityRules      []ValidityRule    `yaml:"validity_rules" json:"validity_rules"`
	PrimaryKey         []string          `yaml:"primary_key" json:"primary_key"`
	UniqueKeys         [][]string        `yaml:"unique_keys" json:"unique_keys"`
	ReferentialChecks  []ReferentialCheck `yaml:"referential_checks" json:"referential_checks"`
	RangeChecks        []RangeCheckConfig `yaml:"range_checks" json:"range_checks"`
}

type ConsistencyRule struct {
	Type       string `yaml:"type" json:"type"`
	FieldA     string `yaml:"field_a" json:"field_a"`
	FieldB     string `yaml:"field_b" json:"field_b"`
	Expression string `yaml:"expression" json:"expression"`
}

type ValidityRule struct {
	Field    string `yaml:"field" json:"field"`
	Pattern  string `yaml:"pattern" json:"pattern"`
	CheckFn  string `yaml:"check_fn" json:"check_fn"`
}

type ReferentialCheck struct {
	ForeignKey string `yaml:"foreign_key" json:"foreign_key"`
	RefTable   string `yaml:"ref_table" json:"ref_table"`
	RefKey     string `yaml:"ref_key" json:"ref_key"`
}

type RangeCheckConfig struct {
	Field string   `yaml:"field" json:"field"`
	Min   *float64 `yaml:"min" json:"min"`
	Max   *float64 `yaml:"max" json:"max"`
}

type Assessor struct {
	Config *QualityConfig
}

func NewAssessor(cfg *QualityConfig) *Assessor {
	if cfg == nil {
		cfg = &QualityConfig{}
	}
	if len(cfg.Weights) == 0 {
		cfg.Weights = map[string]float64{
			"completeness": 1.0 / 6,
			"consistency":  1.0 / 6,
			"accuracy":     1.0 / 6,
			"uniqueness":   1.0 / 6,
			"timeliness":   1.0 / 6,
			"validity":     1.0 / 6,
		}
	}
	return &Assessor{Config: cfg}
}

func (a *Assessor) Assess(ds *datasource.Dataset) *QualityReport {
	report := &QualityReport{
		DatasetName:  ds.Name,
		Timestamp:    time.Now().Format(time.RFC3339),
		TotalRows:    len(ds.Rows),
		TotalColumns: len(ds.Schema.Columns),
		RowQuality:   make([]RowQuality, len(ds.Rows)),
	}

	rowIssues := make([][]string, len(ds.Rows))
	for i := range rowIssues {
		rowIssues[i] = []string{}
	}

	completeness, compDetails := a.assessCompleteness(ds, rowIssues)
	consistency, consDetails := a.assessConsistency(ds, rowIssues)
	accuracy, accDetails := a.assessAccuracy(ds, rowIssues)
	uniqueness, uniqDetails := a.assessUniqueness(ds, rowIssues)
	timeliness, timeDetails := a.assessTimeliness(ds, rowIssues)
	validity, valDetails := a.assessValidity(ds, rowIssues)

	report.Dimensions = []DimensionScore{
		{Dimension: "completeness", Score: completeness, Details: compDetails},
		{Dimension: "consistency", Score: consistency, Details: consDetails},
		{Dimension: "accuracy", Score: accuracy, Details: accDetails},
		{Dimension: "uniqueness", Score: uniqueness, Details: uniqDetails},
		{Dimension: "timeliness", Score: timeliness, Details: timeDetails},
		{Dimension: "validity", Score: validity, Details: valDetails},
	}

	report.DQI = a.calculateDQI(report.Dimensions)

	for i, issues := range rowIssues {
		status := "PASS"
		for _, issue := range issues {
			if isCriticalIssue(issue) {
				status = "FAIL"
				break
			}
			status = "WARN"
		}
		report.RowQuality[i] = RowQuality{
			RowIndex: i,
			Status:   status,
			Issues:   issues,
		}
	}

	report.ColumnScores = a.calculateColumnScores(ds)

	return report
}

func (a *Assessor) assessCompleteness(ds *datasource.Dataset, rowIssues [][]string) (float64, string) {
	totalCells := 0
	nullCells := 0
	colNulls := make(map[string]int)
	colTotals := make(map[string]int)

	pkSet := make(map[string]bool)
	for _, k := range a.Config.PrimaryKey {
		pkSet[k] = true
	}

	for colIdx, col := range ds.Schema.Columns {
		colNulls[col.Name] = 0
		colTotals[col.Name] = len(ds.Rows)
		for rowIdx, row := range ds.Rows {
			totalCells++
			if colIdx >= len(row.Values) || row.Values[colIdx].IsNull {
				nullCells++
				colNulls[col.Name]++
				if !col.Nullable || pkSet[col.Name] {
					rowIssues[rowIdx] = append(rowIssues[rowIdx], fmt.Sprintf("completeness:null_required:%s", col.Name))
				} else {
					rowIssues[rowIdx] = append(rowIssues[rowIdx], fmt.Sprintf("completeness:null:%s", col.Name))
				}
			}
		}
	}

	if totalCells == 0 {
		return 100, "no data"
	}

	overall := float64(totalCells-nullCells) / float64(totalCells) * 100
	details := fmt.Sprintf("Overall: %.1f%% non-null", overall)
	for _, col := range ds.Schema.Columns {
		if colTotals[col.Name] > 0 {
			rate := float64(colTotals[col.Name]-colNulls[col.Name]) / float64(colTotals[col.Name]) * 100
			details += fmt.Sprintf("; %s: %.1f%%", col.Name, rate)
		}
	}

	return overall, details
}

func (a *Assessor) assessConsistency(ds *datasource.Dataset, rowIssues [][]string) (float64, string) {
	if len(a.Config.ConsistencyRules) == 0 {
		return 100, "no consistency rules configured"
	}

	totalChecks := 0
	passedChecks := 0

	for _, rule := range a.Config.ConsistencyRules {
		idxA := ds.Schema.ColumnIndex(rule.FieldA)
		idxB := ds.Schema.ColumnIndex(rule.FieldB)
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

			totalChecks++
			passed := checkConsistencyExpression(rule.Expression, cellA, cellB)
			if passed {
				passedChecks++
			} else {
				rowIssues[rowIdx] = append(rowIssues[rowIdx],
					fmt.Sprintf("consistency:%s_%s:%s", rule.FieldA, rule.FieldB, rule.Expression))
			}
		}
	}

	if totalChecks == 0 {
		return 100, "no checks performed"
	}
	score := float64(passedChecks) / float64(totalChecks) * 100
	return score, fmt.Sprintf("%.1f%% consistent (%d/%d)", score, passedChecks, totalChecks)
}

func checkConsistencyExpression(expr string, a, b datasource.CellValue) bool {
	switch expr {
	case "<=", "a<=b", "A<=B":
		return compareCells(a, b) <= 0
	case ">=", "a>=b", "A>=B":
		return compareCells(a, b) >= 0
	case "<", "a<b", "A<B":
		return compareCells(a, b) < 0
	case ">", "a>b", "A>B":
		return compareCells(a, b) > 0
	case "==", "=", "a==b", "A==B":
		return compareCells(a, b) == 0
	default:
		return true
	}
}

func compareCells(a, b datasource.CellValue) int {
	aVal := cellToFloat(a)
	bVal := cellToFloat(b)
	if aVal < bVal {
		return -1
	}
	if aVal > bVal {
		return 1
	}
	return 0
}

func cellToFloat(c datasource.CellValue) float64 {
	switch c.Type {
	case datasource.TypeInt:
		return float64(c.IntVal)
	case datasource.TypeFloat:
		return c.FloatVal
	case datasource.TypeDate:
		return float64(c.DateVal.Unix())
	case datasource.TypeBool:
		if c.BoolVal {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func (a *Assessor) assessAccuracy(ds *datasource.Dataset, rowIssues [][]string) (float64, string) {
	if len(a.Config.RangeChecks) == 0 {
		return 100, "no accuracy rules configured"
	}

	totalChecks := 0
	passedChecks := 0

	for _, rc := range a.Config.RangeChecks {
		colIdx := ds.Schema.ColumnIndex(rc.Field)
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

			totalChecks++
			val := cellToFloat(cell)
			inRange := true
			if rc.Min != nil && val < *rc.Min {
				inRange = false
			}
			if rc.Max != nil && val > *rc.Max {
				inRange = false
			}

			if inRange {
				passedChecks++
			} else {
				rowIssues[rowIdx] = append(rowIssues[rowIdx],
					fmt.Sprintf("accuracy:range:%s:%v", rc.Field, val))
			}
		}
	}

	if totalChecks == 0 {
		return 100, "no checks performed"
	}
	score := float64(passedChecks) / float64(totalChecks) * 100
	return score, fmt.Sprintf("%.1f%% accurate (%d/%d)", score, passedChecks, totalChecks)
}

func (a *Assessor) assessUniqueness(ds *datasource.Dataset, rowIssues [][]string) (float64, string) {
	keys := a.Config.PrimaryKey
	if len(keys) == 0 {
		for _, uk := range a.Config.UniqueKeys {
			keys = uk
			break
		}
		if len(keys) == 0 {
			return 100, "no primary/unique key configured"
		}
	}

	keyIndices := make([]int, 0, len(keys))
	for _, k := range keys {
		idx := ds.Schema.ColumnIndex(k)
		if idx >= 0 {
			keyIndices = append(keyIndices, idx)
		}
	}

	if len(keyIndices) == 0 {
		return 100, "no key columns found"
	}

	seen := make(map[string][]int)
	for rowIdx, row := range ds.Rows {
		key := makeKey(row, keyIndices)
		seen[key] = append(seen[key], rowIdx)
	}

	totalRows := len(ds.Rows)
	duplicateRows := 0
	for _, indices := range seen {
		if len(indices) > 1 {
			duplicateRows += len(indices) - 1
			for _, idx := range indices[1:] {
				rowIssues[idx] = append(rowIssues[idx], "uniqueness:duplicate_key")
			}
		}
	}

	if totalRows == 0 {
		return 100, "no data"
	}
	score := float64(totalRows-duplicateRows) / float64(totalRows) * 100
	return score, fmt.Sprintf("%.1f%% unique (%d duplicates)", score, duplicateRows)
}

func makeKey(row datasource.Row, indices []int) string {
	parts := make([]string, len(indices))
	for i, idx := range indices {
		if idx < len(row.Values) {
			parts[i] = datasource.FormatCellValue(row.Values[idx])
		} else {
			parts[i] = "NULL"
		}
	}
	k := ""
	for _, p := range parts {
		k += p + "|"
	}
	return k
}

func (a *Assessor) assessTimeliness(ds *datasource.Dataset, rowIssues [][]string) (float64, string) {
	if a.Config.TimelinessThreshold == nil {
		return 100, "no timeliness threshold configured"
	}

	threshold, err := time.ParseDuration(*a.Config.TimelinessThreshold)
	if err != nil {
		return 100, fmt.Sprintf("invalid threshold: %s", *a.Config.TimelinessThreshold)
	}

	dateCols := []int{}
	for i, col := range ds.Schema.Columns {
		if col.DataType == datasource.TypeDate {
			dateCols = append(dateCols, i)
		}
	}

	if len(dateCols) == 0 {
		return 100, "no date columns found"
	}

	now := time.Now()
	totalChecks := 0
	passedChecks := 0

	for _, colIdx := range dateCols {
		for rowIdx, row := range ds.Rows {
			if colIdx >= len(row.Values) {
				continue
			}
			cell := row.Values[colIdx]
			if cell.IsNull {
				continue
			}

			totalChecks++
			age := now.Sub(cell.DateVal)
			if age <= threshold {
				passedChecks++
			} else {
				rowIssues[rowIdx] = append(rowIssues[rowIdx],
					fmt.Sprintf("timeliness:stale:%s", ds.Schema.Columns[colIdx].Name))
			}
		}
	}

	if totalChecks == 0 {
		return 100, "no checks performed"
	}
	score := float64(passedChecks) / float64(totalChecks) * 100
	return score, fmt.Sprintf("%.1f%% timely (%d/%d)", score, passedChecks, totalChecks)
}

func (a *Assessor) assessValidity(ds *datasource.Dataset, rowIssues [][]string) (float64, string) {
	if len(a.Config.ValidityRules) == 0 {
		return 100, "no validity rules configured"
	}

	totalChecks := 0
	passedChecks := 0

	for _, vr := range a.Config.ValidityRules {
		colIdx := ds.Schema.ColumnIndex(vr.Field)
		if colIdx < 0 {
			continue
		}

		pattern := vr.Pattern
		pattern = strings.ReplaceAll(pattern, "\\\\", "\\")
		re, err := regexp.Compile(pattern)
		if err != nil {
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

			totalChecks++
			valStr := datasource.FormatCellValue(cell)
			if re.MatchString(valStr) {
				passedChecks++
			} else {
				rowIssues[rowIdx] = append(rowIssues[rowIdx],
					fmt.Sprintf("validity:pattern:%s:%s", vr.Field, valStr))
			}
		}
	}

	if totalChecks == 0 {
		return 100, "no checks performed"
	}
	score := float64(passedChecks) / float64(totalChecks) * 100
	return score, fmt.Sprintf("%.1f%% valid (%d/%d)", score, passedChecks, totalChecks)
}

func (a *Assessor) calculateDQI(dimensions []DimensionScore) float64 {
	totalWeight := 0.0
	weightedSum := 0.0
	for _, d := range dimensions {
		w, ok := a.Config.Weights[d.Dimension]
		if !ok {
			w = 1.0 / 6
		}
		weightedSum += d.Score + w
		totalWeight += w
	}
	if totalWeight == 0 {
		return 0
	}
	return math.Round(weightedSum * 100) / 100
}

func (a *Assessor) calculateColumnScores(ds *datasource.Dataset) []ColumnScore {
	scores := make([]ColumnScore, len(ds.Schema.Columns))
	for colIdx, col := range ds.Schema.Columns {
		nullCount := 0
		for _, row := range ds.Rows {
			if colIdx >= len(row.Values) || row.Values[colIdx].IsNull {
				nullCount++
			}
		}
		comp := 100.0
		if len(ds.Rows) > 0 {
			comp = float64(len(ds.Rows)-nullCount) / float64(len(ds.Rows)) * 100
		}
		scores[colIdx] = ColumnScore{
			ColumnName:   col.Name,
			Completeness: math.Round(comp*100) / 100,
			Validity:     100,
		}
	}
	return scores
}

func isCriticalIssue(issue string) bool {
	criticalPatterns := []string{"completeness:null_required", "accuracy:range", "uniqueness:duplicate"}
	for _, p := range criticalPatterns {
		if len(issue) >= len(p) && issue[:len(p)] == p {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
