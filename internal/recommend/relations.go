package recommend

import (
	"github.com/data-cleaner/internal/datasource"
)

func AnalyzeRelations(ds *datasource.Dataset, rows []datasource.Row) []ColumnRelation {
	var relations []ColumnRelation

	colNames := ds.Schema.ColumnNames()
	for i, colA := range colNames {
		for j, colB := range colNames {
			if i == j {
				continue
			}

			idxA := ds.Schema.ColumnIndex(colA)
			idxB := ds.Schema.ColumnIndex(colB)

			if fkRel := checkForeignKey(rows, idxA, idxB, colA, colB); fkRel != nil {
				relations = append(relations, *fkRel)
			}

			if fdRel := checkFunctionalDependency(rows, idxA, idxB, colA, colB); fdRel != nil {
				relations = append(relations, *fdRel)
			}

			if timeRel := checkTimeOrder(rows, idxA, idxB, colA, colB); timeRel != nil {
				relations = append(relations, *timeRel)
			}
		}
	}

	return relations
}

func checkForeignKey(rows []datasource.Row, idxA, idxB int, colA, colB string) *ColumnRelation {
	valuesB := make(map[string]bool)
	totalA := 0
	matchedA := 0

	for _, row := range rows {
		if idxB < len(row.Values) && !row.Values[idxB].IsNull {
			valuesB[datasource.FormatCellValue(row.Values[idxB])] = true
		}
	}

	for _, row := range rows {
		if idxA >= len(row.Values) {
			continue
		}
		cellA := row.Values[idxA]
		if cellA.IsNull {
			continue
		}

		totalA++
		valA := datasource.FormatCellValue(cellA)
		if valuesB[valA] {
			matchedA++
		}
	}

	if totalA == 0 {
		return nil
	}

	confidence := float64(matchedA) / float64(totalA)
	if confidence >= 0.95 {
		return &ColumnRelation{
			Type:       RelationForeignKey,
			ColumnA:    colA,
			ColumnB:    colB,
			Confidence: confidence,
			MatchCount: matchedA,
			TotalCount: totalA,
		}
	}

	return nil
}

func checkFunctionalDependency(rows []datasource.Row, idxA, idxB int, colA, colB string) *ColumnRelation {
	valueMap := make(map[string]string)
	total := 0
	matched := 0

	for _, row := range rows {
		if idxA >= len(row.Values) || idxB >= len(row.Values) {
			continue
		}

		cellA := row.Values[idxA]
		cellB := row.Values[idxB]

		if cellA.IsNull || cellB.IsNull {
			continue
		}

		total++
		valA := datasource.FormatCellValue(cellA)
		valB := datasource.FormatCellValue(cellB)

		if existing, ok := valueMap[valA]; ok {
			if existing == valB {
				matched++
			}
		} else {
			valueMap[valA] = valB
			matched++
		}
	}

	if total == 0 {
		return nil
	}

	confidence := float64(matched) / float64(total)
	if confidence >= 0.95 && float64(len(valueMap)) < float64(total)*0.9 {
		return &ColumnRelation{
			Type:       RelationFunctionalDep,
			ColumnA:    colA,
			ColumnB:    colB,
			Confidence: confidence,
			MatchCount: matched,
			TotalCount: total,
		}
	}

	return nil
}

func checkTimeOrder(rows []datasource.Row, idxA, idxB int, colA, colB string) *ColumnRelation {
	total := 0
	matched := 0

	for _, row := range rows {
		if idxA >= len(row.Values) || idxB >= len(row.Values) {
			continue
		}

		cellA := row.Values[idxA]
		cellB := row.Values[idxB]

		if cellA.IsNull || cellB.IsNull {
			continue
		}

		if (cellA.Type != datasource.TypeDate) || (cellB.Type != datasource.TypeDate) {
			continue
		}

		total++
		if !cellA.DateVal.After(cellB.DateVal) {
			matched++
		}
	}

	if total == 0 {
		return nil
	}

	confidence := float64(matched) / float64(total)
	if confidence >= 0.95 {
		return &ColumnRelation{
			Type:       RelationTimeOrder,
			ColumnA:    colA,
			ColumnB:    colB,
			Confidence: confidence,
			MatchCount: matched,
			TotalCount: total,
		}
	}

	return nil
}
