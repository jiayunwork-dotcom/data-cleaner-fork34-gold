package cleaning

import (
	"testing"

	"github.com/data-cleaner/internal/datasource"
)

func TestStandardizeFormats_TrimBothSides(t *testing.T) {
	ds := &datasource.Dataset{
		Name: "t",
		Schema: datasource.Schema{Columns: []datasource.ColumnSchema{
			{Name: "city", DataType: datasource.TypeString},
		}},
		Rows: []datasource.Row{{
			Values: []datasource.CellValue{{
				StrVal: "  paris  ",
				Raw:    "  paris  ",
				Type:   datasource.TypeString,
			}},
		}},
	}
	c := NewCleaner(nil, nil, &FormatConfig{Rules: []FormatRule{
		{Column: "city", Strategy: FormatTrim},
	}}, nil, nil, false)
	out := c.StandardizeFormats(ds)
	got := out.Rows[0].Values[0].StrVal
	if got != "paris" {
		t.Fatalf("trim %q, want %q", got, "paris")
	}
}
