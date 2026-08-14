package recommend

import (
	"fmt"
	"math"

	"github.com/data-cleaner/internal/config"
)

func GenerateRecommendations(result *AnalysisResult, existingRules *config.RulesConfig, existingQuality *config.QualityConfig) []RuleRecommendation {
	var recommendations []RuleRecommendation
	ruleIDCounter := 0

	for _, stats := range result.ColumnStats {
		colRecs := generateColumnRecommendations(stats, result, &ruleIDCounter)
		recommendations = append(recommendations, colRecs...)
	}

	for _, rel := range result.Relations {
		relRecs := generateRelationRecommendations(rel, result, &ruleIDCounter)
		recommendations = append(recommendations, relRecs...)
	}

	recommendations = calculateConfidence(recommendations, result)
	recommendations = filterDeduplicate(recommendations, existingRules, existingQuality)

	return recommendations
}

func generateColumnRecommendations(stats *ColumnStats, result *AnalysisResult, counter *int) []RuleRecommendation {
	var recs []RuleRecommendation

	if stats.NullRate == 0 && stats.TotalRows > 0 {
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:         fmt.Sprintf("rec_not_null_%s_%d", stats.ColumnName, *counter),
			Type:       "not_null",
			Field:      stats.ColumnName,
			Params:     map[string]interface{}{},
			Reason:     fmt.Sprintf("列 %s 空值率为 0%% (共 %d 行)", stats.ColumnName, stats.TotalRows),
			MatchRate:  1.0,
			SampleSize: stats.TotalRows,
		})
	}

	if stats.UniqueRate == 1.0 && stats.TotalRows > 1 {
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:         fmt.Sprintf("rec_unique_%s_%d", stats.ColumnName, *counter),
			Type:       "unique",
			Field:      stats.ColumnName,
			Params:     map[string]interface{}{},
			Reason:     fmt.Sprintf("列 %s 唯一值占比 100%% (%d/%d)", stats.ColumnName, stats.UniqueCount, stats.TotalRows),
			MatchRate:  1.0,
			SampleSize: stats.TotalRows,
		})
	}

	if stats.NumericStats != nil {
		min := stats.NumericStats.Min
		max := stats.NumericStats.Max
		mean := stats.NumericStats.Mean
		std := stats.NumericStats.StdDev

		lowerBound := mean - 3*std
		upperBound := mean + 3*std

		ruleMin := math.Max(min, lowerBound)
		ruleMax := math.Min(max, upperBound)

		if ruleMin > min {
			ruleMin = min
		}
		if ruleMax < max {
			ruleMax = max
		}

		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_range_%s_%d", stats.ColumnName, *counter),
			Type:  "range",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"min": ruleMin,
				"max": ruleMax,
			},
			Reason: fmt.Sprintf("列 %s 为数值型，值域范围 [%.2f, %.2f]，均值±3σ [%.2f, %.2f]",
				stats.ColumnName, min, max, lowerBound, upperBound),
			MatchRate:  0.997,
			SampleSize: stats.TotalRows,
		})
	}

	if stats.UniqueCount > 0 && stats.UniqueCount < 20 && stats.UniqueRate < 0.5 {
		values := make([]interface{}, 0, len(stats.TopValues))
		for _, tv := range stats.TopValues {
			values = append(values, tv.Value)
		}

		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_enum_%s_%d", stats.ColumnName, *counter),
			Type:  "enum",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"values": values,
			},
			Reason: fmt.Sprintf("列 %s 为类别型，共 %d 个唯一值 (<20)", stats.ColumnName, stats.UniqueCount),
			MatchRate:  float64(stats.UniqueCount) / float64(stats.TotalRows-stats.NullCount),
			SampleSize: stats.TotalRows,
		})
	}

	if stats.BestPattern != nil {
		patternRecs := generatePatternRecommendations(stats, result, counter)
		recs = append(recs, patternRecs...)
	}

	if stats.StringStats != nil {
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_length_%s_%d", stats.ColumnName, *counter),
			Type:  "length",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"min": float64(stats.StringStats.MinLength),
				"max": float64(stats.StringStats.MaxLength),
			},
			Reason: fmt.Sprintf("列 %s 字符串长度范围 [%d, %d]，平均 %.1f",
				stats.ColumnName, stats.StringStats.MinLength,
				stats.StringStats.MaxLength, stats.StringStats.AvgLength),
			MatchRate:  1.0,
			SampleSize: stats.TotalRows - stats.NullCount,
		})
	}

	return recs
}

func generatePatternRecommendations(stats *ColumnStats, result *AnalysisResult, counter *int) []RuleRecommendation {
	var recs []RuleRecommendation
	pattern := stats.BestPattern

	switch pattern.Type {
	case PatternEmail:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_regex_email_%s_%d", stats.ColumnName, *counter),
			Type:  "regex",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"pattern": pattern.Pattern,
			},
			Reason: fmt.Sprintf("列 %s 检测到邮箱格式，匹配率 %.1f%%",
				stats.ColumnName, pattern.MatchRate*100),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})

	case PatternPhone:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_regex_phone_%s_%d", stats.ColumnName, *counter),
			Type:  "regex",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"pattern": pattern.Pattern,
			},
			Reason: fmt.Sprintf("列 %s 检测到电话号码格式，匹配率 %.1f%%",
				stats.ColumnName, pattern.MatchRate*100),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})

	case PatternDate:
		dateFormat := "2006-01-02"
		colIdx := result.Schema.ColumnIndex(stats.ColumnName)
		minDate, maxDate, ok := GetDateRange(result.SampledRows, colIdx)
		if ok {
			*counter++
			recs = append(recs, RuleRecommendation{
				ID:   fmt.Sprintf("rec_regex_date_%s_%d", stats.ColumnName, *counter),
				Type: "regex",
				Field: stats.ColumnName,
				Params: map[string]interface{}{
					"pattern": pattern.Pattern,
				},
				Reason: fmt.Sprintf("列 %s 检测到日期格式，匹配率 %.1f%%，范围 %s 至 %s",
					stats.ColumnName, pattern.MatchRate*100,
					minDate.Format(dateFormat), maxDate.Format(dateFormat)),
				MatchRate:         pattern.MatchRate,
				SampleSize:        stats.TotalRows - stats.NullCount,
				PatternConsistency: pattern.MatchRate,
			})

			*counter++
			recs = append(recs, RuleRecommendation{
				ID:   fmt.Sprintf("rec_range_date_%s_%d", stats.ColumnName, *counter),
				Type: "range",
				Field: stats.ColumnName,
				Params: map[string]interface{}{
					"min": float64(minDate.Unix()),
					"max": float64(maxDate.Unix()),
				},
				Reason: fmt.Sprintf("列 %s 日期范围 %s 至 %s",
					stats.ColumnName, minDate.Format(dateFormat), maxDate.Format(dateFormat)),
				MatchRate:  1.0,
				SampleSize: stats.TotalRows - stats.NullCount,
			})
		}

	case PatternIDCard:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_length_idcard_%s_%d", stats.ColumnName, *counter),
			Type:  "length",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"min": float64(18),
				"max": float64(18),
			},
			Reason: fmt.Sprintf("列 %s 检测到身份证号格式，长度应为 18 位", stats.ColumnName),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})

		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_regex_idcard_%s_%d", stats.ColumnName, *counter),
			Type:  "regex",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"pattern": pattern.Pattern,
			},
			Reason: fmt.Sprintf("列 %s 检测到身份证号格式，匹配率 %.1f%%",
				stats.ColumnName, pattern.MatchRate*100),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})

	case PatternURL:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_regex_url_%s_%d", stats.ColumnName, *counter),
			Type:  "regex",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"pattern": pattern.Pattern,
			},
			Reason: fmt.Sprintf("列 %s 检测到 URL 格式，匹配率 %.1f%%",
				stats.ColumnName, pattern.MatchRate*100),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})

	case PatternIP:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_regex_ip_%s_%d", stats.ColumnName, *counter),
			Type:  "regex",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"pattern": pattern.Pattern,
			},
			Reason: fmt.Sprintf("列 %s 检测到 IP 地址格式，匹配率 %.1f%%",
				stats.ColumnName, pattern.MatchRate*100),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})

	case PatternAmount:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:    fmt.Sprintf("rec_regex_amount_%s_%d", stats.ColumnName, *counter),
			Type:  "regex",
			Field: stats.ColumnName,
			Params: map[string]interface{}{
				"pattern": pattern.Pattern,
			},
			Reason: fmt.Sprintf("列 %s 检测到金额格式，匹配率 %.1f%%",
				stats.ColumnName, pattern.MatchRate*100),
			MatchRate:         pattern.MatchRate,
			SampleSize:        stats.TotalRows - stats.NullCount,
			PatternConsistency: pattern.MatchRate,
		})
	}

	return recs
}

func generateRelationRecommendations(rel ColumnRelation, result *AnalysisResult, counter *int) []RuleRecommendation {
	var recs []RuleRecommendation

	switch rel.Type {
	case RelationForeignKey:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:   fmt.Sprintf("rec_fk_%s_%s_%d", rel.ColumnA, rel.ColumnB, *counter),
			Type: "cross_field",
			Field: rel.ColumnA,
			Params: map[string]interface{}{
				"field_a":      rel.ColumnA,
				"field_b":      rel.ColumnB,
				"operator":     "referential_integrity",
			},
			Reason: fmt.Sprintf("检测到外键关系: %s 的值全部存在于 %s 中 (%.1f%%)",
				rel.ColumnA, rel.ColumnB, rel.Confidence*100),
			MatchRate:  rel.Confidence,
			SampleSize: rel.TotalCount,
		})

	case RelationTimeOrder:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:   fmt.Sprintf("rec_consistency_time_%s_%s_%d", rel.ColumnA, rel.ColumnB, *counter),
			Type: "cross_field",
			Field: rel.ColumnA,
			Params: map[string]interface{}{
				"field_a":  rel.ColumnA,
				"field_b":  rel.ColumnB,
				"operator": "<=",
			},
			Reason: fmt.Sprintf("检测到时间先后关系: %s <= %s (%.1f%%)",
				rel.ColumnA, rel.ColumnB, rel.Confidence*100),
			MatchRate:  rel.Confidence,
			SampleSize: rel.TotalCount,
		})

	case RelationFunctionalDep:
		*counter++
		recs = append(recs, RuleRecommendation{
			ID:   fmt.Sprintf("rec_unique_composite_%s_%s_%d", rel.ColumnA, rel.ColumnB, *counter),
			Type: "unique",
			Field: rel.ColumnA,
			Params: map[string]interface{}{
				"composite": []string{rel.ColumnA, rel.ColumnB},
			},
			Reason: fmt.Sprintf("检测到函数依赖: %s -> %s (%.1f%%)，推荐组合键",
				rel.ColumnA, rel.ColumnB, rel.Confidence*100),
			MatchRate:  rel.Confidence,
			SampleSize: rel.TotalCount,
		})
	}

	return recs
}

func calculateConfidence(recommendations []RuleRecommendation, result *AnalysisResult) []RuleRecommendation {
	for i := range recommendations {
		rec := &recommendations[i]

		matchRateScore := rec.MatchRate * 50

		sampleSizeScore := 0.0
		if rec.SampleSize >= 10000 {
			sampleSizeScore = 25
		} else if rec.SampleSize >= 1000 {
			sampleSizeScore = 20
		} else if rec.SampleSize >= 100 {
			sampleSizeScore = 15
		} else if rec.SampleSize >= 10 {
			sampleSizeScore = 10
		} else {
			sampleSizeScore = 5
		}

		patternScore := rec.PatternConsistency * 25
		if rec.PatternConsistency == 0 {
			patternScore = 20
		}

		total := matchRateScore + sampleSizeScore + patternScore
		rec.Confidence = int(math.Min(100, math.Round(total)))
	}

	return recommendations
}

func filterDeduplicate(recommendations []RuleRecommendation, existingRules *config.RulesConfig, existingQuality *config.QualityConfig) []RuleRecommendation {
	existingRuleKeys := make(map[string]bool)

	for _, r := range existingRules.Builtin {
		key := fmt.Sprintf("%s:%s", r.Type, r.Field)
		existingRuleKeys[key] = true

		if r.Type == "regex" {
			existingRuleKeys[fmt.Sprintf("regex:%s", r.Field)] = true
		}
	}

	for _, r := range existingRules.CrossField {
		key := fmt.Sprintf("%s:%s", r.Type, r.Field)
		existingRuleKeys[key] = true
	}

	for _, rc := range existingQuality.RangeChecks {
		key := fmt.Sprintf("range:%s", rc.Field)
		existingRuleKeys[key] = true
	}

	for _, vr := range existingQuality.ValidityRules {
		key := fmt.Sprintf("regex:%s", vr.Field)
		existingRuleKeys[key] = true
	}

	for _, cr := range existingQuality.ConsistencyRules {
		key := fmt.Sprintf("cross_field:%s_%s", cr.FieldA, cr.FieldB)
		existingRuleKeys[key] = true
	}

	var filtered []RuleRecommendation
	for _, rec := range recommendations {
		key := fmt.Sprintf("%s:%s", rec.Type, rec.Field)
		if rec.Type == "cross_field" {
			if fa, ok := rec.Params["field_a"].(string); ok {
				if fb, ok2 := rec.Params["field_b"].(string); ok2 {
					key = fmt.Sprintf("cross_field:%s_%s", fa, fb)
				}
			}
		}

		if !existingRuleKeys[key] {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

func FilterByConfidence(recommendations []RuleRecommendation, minConfidence int) []RuleRecommendation {
	var filtered []RuleRecommendation
	for _, rec := range recommendations {
		if rec.Confidence >= minConfidence {
			filtered = append(filtered, rec)
		}
	}
	return filtered
}

func SortByConfidence(recommendations []RuleRecommendation) []RuleRecommendation {
	sorted := make([]RuleRecommendation, len(recommendations))
	copy(sorted, recommendations)

	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Confidence > sorted[i].Confidence {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}
