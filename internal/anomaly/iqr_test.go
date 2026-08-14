package anomaly

import (
	"testing"

	"github.com/data-cleaner/internal/datasource"
)

func TestDetectIQR_MildOutlier(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 20}
	rows := make([]datasource.Row, len(vals))
	for i, v := range vals {
		rows[i] = datasource.Row{Values: []datasource.CellValue{{
			FloatVal: v,
			Type:     datasource.TypeFloat,
		}}}
	}
	ds := &datasource.Dataset{
		Name: "t",
		Schema: datasource.Schema{Columns: []datasource.ColumnSchema{
			{Name: "n", DataType: datasource.TypeFloat},
		}},
		Rows: rows,
	}
	anoms := DetectIQR(ds)
	found := false
	for _, a := range anoms {
		if a.RowIndex == 10 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 20 to be an IQR outlier, got %#v", anoms)
	}
}
