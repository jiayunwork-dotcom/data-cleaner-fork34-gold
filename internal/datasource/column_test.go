package datasource

import "testing"

func TestColumnIndex_ExactName(t *testing.T) {
	s := Schema{Columns: []ColumnSchema{{Name: "UserID"}}}
	if got := s.ColumnIndex("UserID"); got != 0 {
		t.Fatalf("ColumnIndex(UserID)=%d, want 0", got)
	}
}
