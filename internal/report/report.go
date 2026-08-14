package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/data-cleaner/internal/audit"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/quality"
)

type ComparisonReport struct {
	DatasetName string
	Before      *quality.QualityReport
	After       *quality.QualityReport
	BeforeDS    *datasource.Dataset
	AfterDS     *datasource.Dataset
	AuditEntries []audit.AuditEntry
}

func GenerateComparison(before, after *quality.QualityReport, beforeDS, afterDS *datasource.Dataset, entries []audit.AuditEntry) *ComparisonReport {
	return &ComparisonReport{
		DatasetName: before.DatasetName,
		Before:      before,
		After:       after,
		BeforeDS:    beforeDS,
		AfterDS:     afterDS,
		AuditEntries: entries,
	}
}

func (r *ComparisonReport) PrintTerminal() {
	fmt.Println("\n╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║          DATA QUALITY ASSESSMENT - COMPARISON REPORT        ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")

	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│ Quality Score Changes                                       │")
	fmt.Println("├──────────────┬──────────┬──────────┬──────────┬────────────┤")
	fmt.Println("│ Dimension    │ Before   │ After    │ Change   │ Status     │")
	fmt.Println("├──────────────┼──────────┼──────────┼──────────┼────────────┤")

	beforeMap := make(map[string]float64)
	afterMap := make(map[string]float64)
	for _, d := range r.Before.Dimensions {
		beforeMap[d.Dimension] = d.Score
	}
	for _, d := range r.After.Dimensions {
		afterMap[d.Dimension] = d.Score
	}

	dims := []string{"completeness", "consistency", "accuracy", "uniqueness", "timeliness", "validity"}
	for _, dim := range dims {
		bv := beforeMap[dim]
		av := afterMap[dim]
		change := av - bv
		status := "─"
		if change > 0 {
			status = "↑"
		} else if change < 0 {
			status = "↓"
		}
		fmt.Printf("│ %-12s │ %6.1f   │ %6.1f   │ %+6.1f   │ %s          │\n",
			dim, bv, av, change, status)
	}

	fmt.Println("├──────────────┼──────────┼──────────┼──────────┼────────────┤")
	bDQI := r.Before.DQI
	aDQI := r.After.DQI
	dqiChange := aDQI - bDQI
	status := "─"
	if dqiChange > 0 {
		status = "↑"
	} else if dqiChange < 0 {
		status = "↓"
	}
	fmt.Printf("│ DQI          │ %6.1f   │ %6.1f   │ %+6.1f   │ %s          │\n",
		bDQI, aDQI, dqiChange, status)
	fmt.Println("└──────────────┴──────────┴──────────┴──────────┴────────────┘")

	r.printRadarChart()

	if r.BeforeDS != nil && r.AfterDS != nil {
		r.printDistributionChanges()
	}

	if len(r.AuditEntries) > 0 {
		r.printChangeLog()
	}
}

func (r *ComparisonReport) printRadarChart() {
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│ Radar Chart (Before: ○  After: ●)                          │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	dims := []string{"Compl", "Cons", "Accur", "Uniq", "Time", "Valid"}
	beforeMap := make(map[string]float64)
	afterMap := make(map[string]float64)
	dimKeys := []string{"completeness", "consistency", "accuracy", "uniqueness", "timeliness", "validity"}

	for _, d := range r.Before.Dimensions {
		beforeMap[d.Dimension] = d.Score
	}
	for _, d := range r.After.Dimensions {
		afterMap[d.Dimension] = d.Score
	}

	maxScore := 100.0
	radius := 10

	fmt.Println()
	for y := radius; y >= -radius; y-- {
		line := "     "
		for x := -radius; x <= radius; x++ {
			dist := math.Sqrt(float64(x*x+y*y)) / float64(radius)

			if dist <= 1.0 {
				angle := math.Atan2(float64(y), float64(x))
				if angle < 0 {
					angle += 2 * math.Pi
				}

				sector := int(angle / (2 * math.Pi / 6))
				sector = sector % 6
				if sector < 0 {
					sector += 6
				}

				beforeVal := beforeMap[dimKeys[sector]] / maxScore
				afterVal := afterMap[dimKeys[sector]] / maxScore

				if math.Abs(dist-afterVal) < 0.08 {
					line += "●"
				} else if math.Abs(dist-beforeVal) < 0.08 {
					line += "○"
				} else if dist < 0.25 {
					line += "·"
				} else if dist < 0.5 {
					line += "·"
				} else if dist < 0.75 {
					line += "░"
				} else {
					line += "▒"
				}
			} else {
				line += " "
			}
		}
		fmt.Println(line)
	}

	fmt.Println()
	for i, dim := range dims {
		fmt.Printf("  %s: ○ %.1f  ● %.1f", dim, beforeMap[dimKeys[i]], afterMap[dimKeys[i]])
		if i%3 == 2 {
			fmt.Println()
		} else {
			fmt.Print("  │")
		}
	}
	if len(dims)%3 != 0 {
		fmt.Println()
	}
}

func (r *ComparisonReport) printDistributionChanges() {
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│ Distribution Changes                                        │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	beforeStats := calcDatasetStats(r.BeforeDS)
	afterStats := calcDatasetStats(r.AfterDS)

	fmt.Println("┌──────────────┬──────────────┬──────────────┬──────────────┐")
	fmt.Println("│ Column       │ Metric       │ Before       │ After        │")
	fmt.Println("├──────────────┼──────────────┼──────────────┼──────────────┤")

	for _, col := range r.BeforeDS.Schema.Columns {
		bs, bOk := beforeStats[col.Name]
		as, aOk := afterStats[col.Name]
		if !bOk || !aOk {
			continue
		}

		if col.DataType == datasource.TypeInt || col.DataType == datasource.TypeFloat {
			fmt.Printf("│ %-12s │ %-12s │ %12s │ %12s │\n",
				col.Name, "mean", formatFloat(bs.Mean), formatFloat(as.Mean))
			fmt.Printf("│ %-12s │ %-12s │ %12s │ %12s │\n",
				"", "std", formatFloat(bs.StdDev), formatFloat(as.StdDev))
			fmt.Printf("│ %-12s │ %-12s │ %12s │ %12s │\n",
				"", "p25", formatFloat(bs.P25), formatFloat(as.P25))
			fmt.Printf("│ %-12s │ %-12s │ %12s │ %12s │\n",
				"", "p50", formatFloat(bs.P50), formatFloat(as.P50))
			fmt.Printf("│ %-12s │ %-12s │ %12s │ %12s │\n",
				"", "p75", formatFloat(bs.P75), formatFloat(as.P75))
		} else {
			fmt.Printf("│ %-12s │ %-12s │ %12d │ %12d │\n",
				col.Name, "unique", bs.UniqueCount, as.UniqueCount)
		}
	}
	fmt.Println("└──────────────┴──────────────┴──────────────┴──────────────┘")
}

func (r *ComparisonReport) printChangeLog() {
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Printf("│ Change Log (%d modifications)                         │\n", len(r.AuditEntries))
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	limit := 20
	if len(r.AuditEntries) < limit {
		limit = len(r.AuditEntries)
	}

	fmt.Println("┌────────┬──────────────┬────────────────┬────────────────┬──────────────┐")
	fmt.Println("│ Row    │ Column       │ Old Value      │ New Value      │ Strategy     │")
	fmt.Println("├────────┼──────────────┼────────────────┼────────────────┼──────────────┤")

	for i := 0; i < limit; i++ {
		e := r.AuditEntries[i]
		oldVal := e.OldValue
		newVal := e.NewValue
		if len(oldVal) > 14 {
			oldVal = oldVal[:14]
		}
		if len(newVal) > 14 {
			newVal = newVal[:14]
		}
		fmt.Printf("│ %6d │ %-12s │ %-14s │ %-14s │ %-12s │\n",
			e.RowIdx, e.Column, oldVal, newVal, e.Rule)
	}

	if len(r.AuditEntries) > limit {
		fmt.Printf("│ ... and %d more entries                                           │\n",
			len(r.AuditEntries)-limit)
	}
	fmt.Println("└────────┴──────────────┴────────────────┴────────────────┴──────────────┘")
}

type ColumnStats struct {
	Mean       float64
	StdDev     float64
	P25        float64
	P50        float64
	P75        float64
	UniqueCount int
}

func calcDatasetStats(ds *datasource.Dataset) map[string]*ColumnStats {
	stats := make(map[string]*ColumnStats)

	for colIdx, col := range ds.Schema.Columns {
		cs := &ColumnStats{}

		if col.DataType == datasource.TypeInt || col.DataType == datasource.TypeFloat {
			var values []float64
			for _, row := range ds.Rows {
				if colIdx < len(row.Values) && !row.Values[colIdx].IsNull {
					v := cellToFloatVal(row.Values[colIdx])
					if !math.IsNaN(v) && !math.IsInf(v, 0) {
						values = append(values, v)
					}
				}
			}
			if len(values) > 0 {
				sort.Float64s(values)
				cs.Mean = calcMean(values)
				cs.StdDev = calcStdDev(values, cs.Mean)
				cs.P25 = percentileVal(values, 25)
				cs.P50 = percentileVal(values, 50)
				cs.P75 = percentileVal(values, 75)
			}
		} else {
			uniqueSet := make(map[string]bool)
			for _, row := range ds.Rows {
				if colIdx < len(row.Values) && !row.Values[colIdx].IsNull {
					uniqueSet[datasource.FormatCellValue(row.Values[colIdx])] = true
				}
			}
			cs.UniqueCount = len(uniqueSet)
		}

		stats[col.Name] = cs
	}

	return stats
}

func (r *ComparisonReport) WriteHTML(outDir string) error {
	os.MkdirAll(outDir, 0755)

	tmpl := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Data Quality Report - {{.DatasetName}}</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 20px; background: #f5f5f5; }
h1 { color: #333; border-bottom: 2px solid #4a9eff; padding-bottom: 10px; }
h2 { color: #555; margin-top: 30px; }
table { border-collapse: collapse; width: 100%; margin: 10px 0; background: white; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
th, td { border: 1px solid #ddd; padding: 10px 14px; text-align: left; }
th { background: #4a9eff; color: white; }
tr:nth-child(even) { background: #f9f9f9; }
.improved { color: #2e7d32; font-weight: bold; }
.declined { color: #c62828; font-weight: bold; }
.unchanged { color: #666; }
.radar-container { text-align: center; margin: 20px 0; }
canvas { border: 1px solid #ddd; background: white; }
</style>
</head>
<body>
<h1>Data Quality Report: {{.DatasetName}}</h1>

<h2>Quality Score Comparison</h2>
<table>
<tr><th>Dimension</th><th>Before</th><th>After</th><th>Change</th></tr>
{{range .Dimensions}}
<tr>
<td>{{.Name}}</td>
<td>{{printf "%.1f" .Before}}</td>
<td>{{printf "%.1f" .After}}</td>
<td class="{{.Class}}">{{printf "%+.1f" .Change}}</td>
</tr>
{{end}}
</table>

<h2>DQI Score</h2>
<table>
<tr><th>Metric</th><th>Value</th></tr>
<tr><td>Before DQI</td><td>{{printf "%.1f" .BeforeDQI}}</td></tr>
<tr><td>After DQI</td><td>{{printf "%.1f" .AfterDQI}}</td></tr>
<tr><td>Change</td><td class="{{if gt .DQIChange 0.0}}improved{{else if lt .DQIChange 0.0}}declined{{else}}unchanged{{end}}">{{printf "%+.1f" .DQIChange}}</td></tr>
</table>

<h2>Radar Chart</h2>
<div class="radar-container">
<canvas id="radarChart" width="500" height="500"></canvas>
</div>

<script>
var dims = [{{range $i, $d := .Dimensions}}{{if $i}},{{end}}"{{$d.Name}}"{{end}}];
var before = [{{range $i, $d := .Dimensions}}{{if $i}},{{end}}{{$d.Before}}{{end}}];
var after = [{{range $i, $d := .Dimensions}}{{if $i}},{{end}}{{$d.After}}{{end}}];

var canvas = document.getElementById('radarChart');
var ctx = canvas.getContext('2d');
var cx = 250, cy = 250, r = 180;

ctx.clearRect(0, 0, 500, 500);

for (var i = 0; i <= 4; i++) {
  var level = r * (i + 1) / 5;
  ctx.beginPath();
  for (var j = 0; j < dims.length; j++) {
    var angle = (Math.PI * 2 * j / dims.length) - Math.PI / 2;
    var x = cx + level * Math.cos(angle);
    var y = cy + level * Math.sin(angle);
    if (j === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  }
  ctx.closePath();
  ctx.strokeStyle = '#ddd';
  ctx.stroke();
}

for (var j = 0; j < dims.length; j++) {
  var angle = (Math.PI * 2 * j / dims.length) - Math.PI / 2;
  ctx.beginPath();
  ctx.moveTo(cx, cy);
  ctx.lineTo(cx + r * Math.cos(angle), cy + r * Math.sin(angle));
  ctx.strokeStyle = '#ddd';
  ctx.stroke();

  var lx = cx + (r + 25) * Math.cos(angle);
  var ly = cy + (r + 25) * Math.sin(angle);
  ctx.fillStyle = '#333';
  ctx.font = '12px sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText(dims[j], lx, ly);
}

ctx.beginPath();
for (var j = 0; j < dims.length; j++) {
  var angle = (Math.PI * 2 * j / dims.length) - Math.PI / 2;
  var val = before[j] / 100 * r;
  var x = cx + val * Math.cos(angle);
  var y = cy + val * Math.sin(angle);
  if (j === 0) ctx.moveTo(x, y);
  else ctx.lineTo(x, y);
}
ctx.closePath();
ctx.strokeStyle = 'rgba(74, 158, 255, 0.8)';
ctx.fillStyle = 'rgba(74, 158, 255, 0.15)';
ctx.fill();
ctx.stroke();

ctx.beginPath();
for (var j = 0; j < dims.length; j++) {
  var angle = (Math.PI * 2 * j / dims.length) - Math.PI / 2;
  var val = after[j] / 100 * r;
  var x = cx + val * Math.cos(angle);
  var y = cy + val * Math.sin(angle);
  if (j === 0) ctx.moveTo(x, y);
  else ctx.lineTo(x, y);
}
ctx.closePath();
ctx.strokeStyle = 'rgba(46, 125, 50, 0.8)';
ctx.fillStyle = 'rgba(46, 125, 50, 0.15)';
ctx.fill();
ctx.stroke();
</script>

{{if .ChangeCount}}
<h2>Change Log ({{.ChangeCount}} modifications)</h2>
<table>
<tr><th>Row</th><th>Column</th><th>Old Value</th><th>New Value</th><th>Strategy</th><th>Timestamp</th></tr>
{{range .Changes}}
<tr>
<td>{{.RowIdx}}</td>
<td>{{.Column}}</td>
<td>{{.OldValue}}</td>
<td>{{.NewValue}}</td>
<td>{{.Rule}}</td>
<td>{{.Timestamp}}</td>
</tr>
{{end}}
</table>
{{end}}

</body>
</html>`

	type dimData struct {
		Name   string
		Before float64
		After  float64
		Change float64
		Class  string
	}

	beforeMap := make(map[string]float64)
	afterMap := make(map[string]float64)
	for _, d := range r.Before.Dimensions {
		beforeMap[d.Dimension] = d.Score
	}
	for _, d := range r.After.Dimensions {
		afterMap[d.Dimension] = d.Score
	}

	var dimDataList []dimData
	for _, key := range []string{"completeness", "consistency", "accuracy", "uniqueness", "timeliness", "validity"} {
		bv := beforeMap[key]
		av := afterMap[key]
		change := av - bv
		cls := "unchanged"
		if change > 0 {
			cls = "improved"
		} else if change < 0 {
			cls = "declined"
		}
		dimDataList = append(dimDataList, dimData{Name: key, Before: bv, After: av, Change: change, Class: cls})
	}

	data := struct {
		DatasetName string
		Dimensions  []dimData
		BeforeDQI   float64
		AfterDQI    float64
		DQIChange   float64
		Changes     []audit.AuditEntry
		ChangeCount int
	}{
		DatasetName: r.DatasetName,
		Dimensions:  dimDataList,
		BeforeDQI:   r.Before.DQI,
		AfterDQI:    r.After.DQI,
		DQIChange:   r.After.DQI - r.Before.DQI,
		Changes:     r.AuditEntries,
		ChangeCount: len(r.AuditEntries),
	}

	t, err := template.New("report").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	f, err := os.Create(filepath.Join(outDir, "report.html"))
	if err != nil {
		return fmt.Errorf("create html: %w", err)
	}
	defer f.Close()

	return t.Execute(f, data)
}

func WriteQualityReport(rpt *quality.QualityReport, outDir string) error {
	os.MkdirAll(outDir, 0755)
	data, err := json.MarshalIndent(rpt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "quality_report.json"), data, 0644)
}

func PrintQualityReport(rpt *quality.QualityReport) {
	fmt.Println("\n╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║            DATA QUALITY ASSESSMENT REPORT                   ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")

	fmt.Printf("\nDataset: %s\n", rpt.DatasetName)
	fmt.Printf("Timestamp: %s\n", rpt.Timestamp)
	fmt.Printf("Rows: %d  Columns: %d\n", rpt.TotalRows, rpt.TotalColumns)

	fmt.Println("\n┌──────────────┬──────────┬──────────────────────────────────────────────┐")
	fmt.Println("│ Dimension    │ Score    │ Details                                      │")
	fmt.Println("├──────────────┼──────────┼──────────────────────────────────────────────┤")

	for _, d := range rpt.Dimensions {
		details := d.Details
		if len(details) > 44 {
			details = details[:44] + "..."
		}
		fmt.Printf("│ %-12s │ %6.1f   │ %-44s │\n", d.Dimension, d.Score, details)
	}

	fmt.Println("├──────────────┼──────────┼──────────────────────────────────────────────┤")
	fmt.Printf("│ DQI          │ %6.1f   │ Weighted average of all dimensions           │\n", rpt.DQI)
	fmt.Println("└──────────────┴──────────┴──────────────────────────────────────────────┘")

	passCount := 0
	warnCount := 0
	failCount := 0
	for _, rq := range rpt.RowQuality {
		switch rq.Status {
		case "PASS":
			passCount++
		case "WARN":
			warnCount++
		case "FAIL":
			failCount++
		}
	}

	fmt.Println("\n┌──────────────────────────┐")
	fmt.Println("│ Row Quality Summary      │")
	fmt.Println("├──────────────┬───────────┤")
	fmt.Printf("│ PASS         │ %9d │\n", passCount)
	fmt.Printf("│ WARN         │ %9d │\n", warnCount)
	fmt.Printf("│ FAIL         │ %9d │\n", failCount)
	fmt.Println("└──────────────┴───────────┘")
}

func formatFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "N/A"
	}
	s := fmt.Sprintf("%.2f", f)
	return s
}

func cellToFloatVal(cell datasource.CellValue) float64 {
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

func percentileVal(sorted []float64, p float64) float64 {
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

func makeKey(row datasource.Row, indices []int) string {
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
