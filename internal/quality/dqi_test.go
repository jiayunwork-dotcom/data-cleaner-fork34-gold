package quality

import "testing"

func TestCalculateDQI_WeightedAverage(t *testing.T) {
	a := NewAssessor(nil)
	dims := []DimensionScore{
		{Dimension: "completeness", Score: 100},
		{Dimension: "consistency", Score: 100},
		{Dimension: "accuracy", Score: 100},
		{Dimension: "uniqueness", Score: 100},
		{Dimension: "timeliness", Score: 100},
		{Dimension: "validity", Score: 100},
	}
	got := a.calculateDQI(dims)
	if got != 100 {
		t.Fatalf("DQI=%v, want 100", got)
	}
}
