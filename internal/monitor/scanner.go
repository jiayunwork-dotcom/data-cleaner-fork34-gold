package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/data-cleaner/internal/anomaly"
	"github.com/data-cleaner/internal/config"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/quality"
)

type ScanResult struct {
	TaskName   string                `json:"task_name"`
	Timestamp  time.Time             `json:"timestamp"`
	DQI        float64               `json:"dqi"`
	Dimensions map[string]float64    `json:"dimensions"`
	NullRates  map[string]float64    `json:"null_rates"`
	AnomalyCounts map[string]int     `json:"anomaly_counts"`
	ColumnScores  map[string]float64 `json:"column_scores"`
	TotalRows     int                `json:"total_rows"`
	TotalColumns  int                `json:"total_columns"`
}

type HistoryRing struct {
	mu      sync.Mutex
	records []*ScanResult
	cap     int
	head    int
	count   int
}

func NewHistoryRing(cap int) *HistoryRing {
	if cap <= 0 {
		cap = 100
	}
	return &HistoryRing{
		records: make([]*ScanResult, cap),
		cap:     cap,
	}
}

func (r *HistoryRing) Add(result *ScanResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records[r.head] = result
	r.head = (r.head + 1) % r.cap
	if r.count < r.cap {
		r.count++
	}
}

func (r *HistoryRing) Recent(n int) []*ScanResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if n > r.count {
		n = r.count
	}
	result := make([]*ScanResult, n)
	for i := 0; i < n; i++ {
		idx := (r.head - 1 - i + r.cap) % r.cap
		result[i] = r.records[idx]
	}
	return result
}

func (r *HistoryRing) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

type Scanner struct {
	cfg        *config.Config
	history    map[string]*HistoryRing
	sem        chan struct{}
	mu         sync.Mutex
	archiveDir string
}

func NewScanner(cfg *config.Config, poolSize int, cacheDir string) *Scanner {
	if poolSize <= 0 {
		poolSize = 5
	}
	archiveDir := filepath.Join(cacheDir, "monitor", "archive")
	os.MkdirAll(archiveDir, 0755)

	s := &Scanner{
		cfg:        cfg,
		history:    make(map[string]*HistoryRing),
		sem:        make(chan struct{}, poolSize),
		archiveDir: archiveDir,
	}

	for _, task := range cfg.Monitor.Tasks {
		s.history[task.Name] = NewHistoryRing(100)
		s.loadArchivedHistory(task.Name)
	}

	return s
}

func (s *Scanner) loadArchivedHistory(taskName string) {
	dir := filepath.Join(s.archiveDir, taskName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var loaded []*ScanResult
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var result ScanResult
		if err := json.Unmarshal(data, &result); err != nil {
			continue
		}
		loaded = append(loaded, &result)
	}

	ring := s.history[taskName]
	for _, r := range loaded {
		ring.Add(r)
	}
}

func (s *Scanner) Scan(taskName string) (*ScanResult, error) {
	var task *config.MonitorTask
	for _, t := range s.cfg.Monitor.Tasks {
		if t.Name == taskName {
			task = &t
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("monitor task '%s' not found", taskName)
	}

	var srcConfig *config.SourceConfig
	for _, src := range s.cfg.Sources {
		if src.Name == task.Source {
			srcConfig = &src
			break
		}
	}
	if srcConfig == nil {
		return nil, fmt.Errorf("source '%s' not found for task '%s'", task.Source, taskName)
	}

	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ds, err := s.loadSource(srcConfig)
	if err != nil {
		return nil, fmt.Errorf("load source '%s': %w", task.Source, err)
	}

	qc := buildQualityConfig(s.cfg)
	assessor := quality.NewAssessor(qc)
	report := assessor.Assess(ds)

	anomalies := anomaly.DetectIQR(ds)
	anomalyCounts := make(map[string]int)
	for _, a := range anomalies {
		anomalyCounts[a.Column]++
	}

	nullRates := make(map[string]float64)
	columnScores := make(map[string]float64)
	for _, cs := range report.ColumnScores {
		columnScores[cs.ColumnName] = cs.Completeness
		totalRows := len(ds.Rows)
		if totalRows > 0 {
			nullRates[cs.ColumnName] = 100 - cs.Completeness
		}
	}

	dimensions := make(map[string]float64)
	for _, d := range report.Dimensions {
		dimensions[d.Dimension] = d.Score
	}

	result := &ScanResult{
		TaskName:      taskName,
		Timestamp:     time.Now(),
		DQI:           report.DQI,
		Dimensions:    dimensions,
		NullRates:     nullRates,
		AnomalyCounts: anomalyCounts,
		ColumnScores:  columnScores,
		TotalRows:     report.TotalRows,
		TotalColumns:  report.TotalColumns,
	}

	ring, ok := s.history[taskName]
	if !ok {
		ring = NewHistoryRing(100)
		s.history[taskName] = ring
	}

	if ring.Count() >= 100 {
		s.archiveOldest(taskName, ring)
	}
	ring.Add(result)

	return result, nil
}

func (s *Scanner) archiveOldest(taskName string, ring *HistoryRing) {
	oldest := ring.Recent(1)
	if len(oldest) == 0 {
		return
	}
	r := oldest[0]
	dir := filepath.Join(s.archiveDir, taskName)
	os.MkdirAll(dir, 0755)
	filename := fmt.Sprintf("scan_%s.json", r.Timestamp.Format("20060102_150405"))
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, filename), data, 0644)
}

func (s *Scanner) GetHistory(taskName string, n int) []*ScanResult {
	ring, ok := s.history[taskName]
	if !ok {
		return nil
	}
	return ring.Recent(n)
}

func (s *Scanner) GetMetricValue(result *ScanResult, metric string) (float64, error) {
	switch metric {
	case "dqi":
		return result.DQI, nil
	case "completeness":
		if v, ok := result.Dimensions["completeness"]; ok {
			return v, nil
		}
	case "consistency":
		if v, ok := result.Dimensions["consistency"]; ok {
			return v, nil
		}
	case "accuracy":
		if v, ok := result.Dimensions["accuracy"]; ok {
			return v, nil
		}
	case "uniqueness":
		if v, ok := result.Dimensions["uniqueness"]; ok {
			return v, nil
		}
	case "timeliness":
		if v, ok := result.Dimensions["timeliness"]; ok {
			return v, nil
		}
	case "validity":
		if v, ok := result.Dimensions["validity"]; ok {
			return v, nil
		}
	default:
		if v, ok := result.NullRates[metric]; ok {
			return v, nil
		}
		if v, ok := result.AnomalyCounts[metric]; ok {
			return float64(v), nil
		}
		if v, ok := result.ColumnScores[metric]; ok {
			return v, nil
		}
	}
	return 0, fmt.Errorf("metric '%s' not found in scan result", metric)
}

func (s *Scanner) loadSource(src *config.SourceConfig) (*datasource.Dataset, error) {
	switch src.Type {
	case "csv":
		return datasource.ReadCSV(src.Path)
	case "json":
		return datasource.ReadJSON(src.Path)
	case "excel":
		return datasource.ReadExcel(src.Path, src.Sheet)
	case "parquet":
		return datasource.ReadParquet(src.Path)
	case "database":
		return datasource.ReadDatabase(src.Database)
	case "api":
		return datasource.ReadAPI(src.API)
	default:
		return nil, fmt.Errorf("unknown source type: %s", src.Type)
	}
}

func buildQualityConfig(cfg *config.Config) *quality.QualityConfig {
	return &quality.QualityConfig{
		Weights:           cfg.Quality.Weights,
		PrimaryKey:        cfg.Quality.PrimaryKey,
		UniqueKeys:        cfg.Quality.UniqueKeys,
		RangeChecks:       cfg.Quality.RangeChecks,
		ConsistencyRules:  cfg.Quality.ConsistencyRules,
		ValidityRules:     cfg.Quality.ValidityRules,
		ReferentialChecks: cfg.Quality.ReferentialChecks,
		TimelinessThreshold: cfg.Quality.TimelinessThreshold,
	}
}
