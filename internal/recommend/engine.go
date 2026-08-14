package recommend

import (
	"math/rand"
	"time"

	"github.com/data-cleaner/internal/config"
	"github.com/data-cleaner/internal/datasource"
)

const (
	FullScanThreshold    = 100000
	DefaultSampleSize    = 10000
	PatternMatchThreshold = 0.90
	DefaultMinConfidence = 70
)

type RecommendEngine struct {
	cfg *RecommendConfig
}

func NewRecommendEngine(cfg *RecommendConfig) *RecommendEngine {
	if cfg == nil {
		cfg = &RecommendConfig{
			MinConfidence: DefaultMinConfidence,
		}
	}
	if cfg.MinConfidence == 0 {
		cfg.MinConfidence = DefaultMinConfidence
	}
	return &RecommendEngine{cfg: cfg}
}

func (e *RecommendEngine) AnalyzeAndRecommend(ds *datasource.Dataset, cfg *config.Config) (*AnalysisResult, []RuleRecommendation) {
	analysisResult := e.Analyze(ds)
	recommendations := GenerateRecommendations(analysisResult, &cfg.Rules, &cfg.Quality)
	recommendations = FilterByConfidence(recommendations, e.cfg.MinConfidence)
	recommendations = SortByConfidence(recommendations)

	if len(e.cfg.FocusColumns) > 0 {
		recommendations = e.filterByFocusColumns(recommendations)
	}

	return analysisResult, recommendations
}

func (e *RecommendEngine) Analyze(ds *datasource.Dataset) *AnalysisResult {
	result := &AnalysisResult{
		ColumnStats: make(map[string]*ColumnStats),
		TotalRows:   len(ds.Rows),
		Schema:      &ds.Schema,
		AllRows:     ds.Rows,
	}

	sampledRows := ds.Rows
	isSampled := false

	if len(ds.Rows) > FullScanThreshold {
		isSampled = true
		sampledRows = sampleRows(ds.Rows, DefaultSampleSize)
		result.SampleSize = DefaultSampleSize
	} else {
		result.SampleSize = len(ds.Rows)
	}

	result.IsSampled = isSampled
	result.SampledRows = sampledRows

	focusSet := make(map[string]bool)
	for _, col := range e.cfg.FocusColumns {
		focusSet[col] = true
	}

	for colIdx, col := range ds.Schema.Columns {
		if len(e.cfg.FocusColumns) > 0 && !focusSet[col.Name] {
			continue
		}

		fullStats := AnalyzeColumnFullScan(ds, colIdx, col.Name, col.DataType)

		var stats *ColumnStats
		if isSampled {
			stats = AnalyzeColumn(sampledRows, colIdx, col.Name, col.DataType, true)
			stats.NullCount = fullStats.NullCount
			stats.NullRate = fullStats.NullRate
			stats.UniqueCount = fullStats.UniqueCount
			stats.UniqueRate = fullStats.UniqueRate
			stats.TotalRows = fullStats.TotalRows
		} else {
			stats = fullStats
		}

		patterns := DetectPatterns(sampledRows, colIdx)
		stats.PatternMatches = patterns
		stats.BestPattern = GetBestPattern(patterns, PatternMatchThreshold)

		result.ColumnStats[col.Name] = stats
	}

	analyzeRows := sampledRows
	if len(e.cfg.FocusColumns) > 0 {
		analyzeRows = sampledRows
	}
	result.Relations = AnalyzeRelations(ds, analyzeRows)

	return result
}

func sampleRows(rows []datasource.Row, sampleSize int) []datasource.Row {
	if len(rows) <= sampleSize {
		return rows
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	indices := r.Perm(len(rows))[:sampleSize]

	sampled := make([]datasource.Row, sampleSize)
	for i, idx := range indices {
		sampled[i] = rows[idx]
	}

	return sampled
}

func (e *RecommendEngine) filterByFocusColumns(recommendations []RuleRecommendation) []RuleRecommendation {
	focusSet := make(map[string]bool)
	for _, col := range e.cfg.FocusColumns {
		focusSet[col] = true
	}

	var filtered []RuleRecommendation
	for _, rec := range recommendations {
		if focusSet[rec.Field] {
			filtered = append(filtered, rec)
			continue
		}

		if fa, ok := rec.Params["field_a"].(string); ok && focusSet[fa] {
			filtered = append(filtered, rec)
			continue
		}
		if fb, ok := rec.Params["field_b"].(string); ok && focusSet[fb] {
			filtered = append(filtered, rec)
			continue
		}
	}

	return filtered
}
