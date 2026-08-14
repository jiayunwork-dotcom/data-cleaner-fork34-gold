package pipeline

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/data-cleaner/internal/audit"
	"github.com/data-cleaner/internal/cleaning"
	"github.com/data-cleaner/internal/config"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/lineage"
	"github.com/data-cleaner/internal/quality"
)

type StepResult struct {
	Name      string
	Status    string
	Duration  time.Duration
	Error     error
	OutputDS  *datasource.Dataset
	Report    *quality.QualityReport
}

type Executor struct {
	Cfg          *config.Config
	Datasets     map[string]*datasource.Dataset
	StepResults  map[string]*StepResult
	AuditLog     *audit.Logger
	CacheDir     string
	DryRun       bool
	LineageTracker *lineage.LineageTracker
	LineageStorage *lineage.LineageStorage
	mu           sync.Mutex
}

func NewExecutor(cfg *config.Config, auditLog *audit.Logger, dryRun bool) *Executor {
	cacheDir := cfg.Cache.Directory
	if cacheDir == "" {
		cacheDir = ".data-cleaner-cache/"
	}
	if cfg.Cache.Enabled {
		os.MkdirAll(cacheDir, 0755)
	}

	historyCount := 10
	if cfg.Lineage.HistoryCount > 0 {
		historyCount = cfg.Lineage.HistoryCount
	}

	return &Executor{
		Cfg:            cfg,
		Datasets:       make(map[string]*datasource.Dataset),
		StepResults:    make(map[string]*StepResult),
		AuditLog:       auditLog,
		CacheDir:       cacheDir,
		DryRun:         dryRun,
		LineageTracker: lineage.NewLineageTracker(),
		LineageStorage: lineage.NewLineageStorage(cacheDir, historyCount),
	}
}

func NewExecutorIncremental(cfg *config.Config, auditLog *audit.Logger, dryRun bool, prevGraph *lineage.LineageGraph) *Executor {
	exec := NewExecutor(cfg, auditLog, dryRun)
	exec.LineageTracker = lineage.NewLineageTrackerIncremental(prevGraph)
	return exec
}

func (e *Executor) LoadSources() error {
	for _, src := range e.Cfg.Sources {
		var ds *datasource.Dataset
		var err error

		switch src.Type {
		case "csv":
			ds, err = datasource.ReadCSV(src.Path)
		case "json":
			ds, err = datasource.ReadJSON(src.Path)
		case "excel":
			ds, err = datasource.ReadExcel(src.Path, src.Sheet)
		case "parquet":
			ds, err = datasource.ReadParquet(src.Path)
		case "database":
			ds, err = datasource.ReadDatabase(src.Database)
		case "api":
			ds, err = datasource.ReadAPI(src.API)
		default:
			return fmt.Errorf("unknown source type: %s", src.Type)
		}

		if err != nil {
			return fmt.Errorf("load source '%s': %w", src.Name, err)
		}
		e.Datasets[src.Name] = ds

		e.LineageTracker.AddSourceNode(src.Name, ds)
	}

	if len(e.Cfg.Sources) > 1 {
		var allDS []*datasource.Dataset
		for _, src := range e.Cfg.Sources {
			if ds, ok := e.Datasets[src.Name]; ok {
				allDS = append(allDS, ds)
			}
		}
		merged, err := datasource.MergeDatasets(allDS)
		if err != nil {
			return fmt.Errorf("merge datasets: %w", err)
		}
		e.Datasets["merged"] = merged

		e.LineageTracker.AddTransformNode("merged", merged)
		columnLineage := lineage.GenerateMergeColumnLineage(allDS, merged, "merge")
		for _, src := range e.Cfg.Sources {
			e.LineageTracker.AddEdge(src.Name, "merged", lineage.TransformMerge, "merge", len(merged.Rows), merged.Schema.ColumnNames(), columnLineage)
		}
	}

	return nil
}

func (e *Executor) Execute() error {
	steps := e.Cfg.Pipeline.Steps
	if len(steps) == 0 {
		return fmt.Errorf("no pipeline steps defined")
	}

	sorted, err := topologicalSort(steps)
	if err != nil {
		return err
	}

	maxWorkers := e.Cfg.Pipeline.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = runtime.NumCPU()
	}

	completed := make(map[string]bool)
	failed := make(map[string]bool)

	for len(completed)+len(failed) < len(sorted) {
		var ready []config.StepConfig
		for _, step := range steps {
			if completed[step.Name] || failed[step.Name] {
				continue
			}
			allDepsMet := true
			for _, dep := range step.DependsOn {
				if !completed[dep] {
					allDepsMet = false
					break
				}
			}
			if allDepsMet {
				ready = append(ready, step)
			}
		}

		if len(ready) == 0 {
			return fmt.Errorf("pipeline stuck: no steps can execute (possible unreported dependency failure)")
		}

		sem := make(chan struct{}, maxWorkers)
		var wg sync.WaitGroup
		var execErrors []error
		var errMu sync.Mutex

		for _, step := range ready {
			sem <- struct{}{}
			wg.Add(1)
			go func(s config.StepConfig) {
				defer wg.Done()
				defer func() { <-sem }()

				if e.hasCachedResult(s) {
					cachedDS := e.loadCachedDataset(s)
					e.mu.Lock()
					if cachedDS != nil {
						e.StepResults[s.Name] = &StepResult{Name: s.Name, Status: "cached", OutputDS: cachedDS}
						e.Datasets[s.Name] = cachedDS
						e.recordCachedStepLineage(s, cachedDS)
					}
					completed[s.Name] = true
					e.mu.Unlock()
					return
				}

				result := e.executeStep(s)
				e.mu.Lock()
				e.StepResults[s.Name] = result
				e.mu.Unlock()

				if result.Error != nil {
					errMu.Lock()
					execErrors = append(execErrors, result.Error)
					errMu.Unlock()

					switch e.Cfg.Pipeline.ErrorPolicy {
					case "abort":
						e.mu.Lock()
						failed[s.Name] = true
						e.mu.Unlock()
					case "continue":
						e.mu.Lock()
						failed[s.Name] = true
						e.mu.Unlock()
					case "retry":
						for retry := 0; retry < e.Cfg.Pipeline.RetryCount; retry++ {
							retryResult := e.executeStep(s)
							if retryResult.Error == nil {
								e.mu.Lock()
								e.StepResults[s.Name] = retryResult
								completed[s.Name] = true
								e.mu.Unlock()
								break
							}
						}
						if !completed[s.Name] {
							e.mu.Lock()
							failed[s.Name] = true
							e.mu.Unlock()
						}
					}
				} else {
					e.mu.Lock()
					completed[s.Name] = true
					e.mu.Unlock()
					e.cacheStepResult(s)
				}
			}(step)
		}
		wg.Wait()

		if len(execErrors) > 0 && e.Cfg.Pipeline.ErrorPolicy == "abort" {
			return execErrors[0]
		}
	}

	return nil
}

func (e *Executor) executeStep(step config.StepConfig) *StepResult {
	start := time.Now()
	result := &StepResult{Name: step.Name}

	if e.DryRun {
		result.Status = "skipped_dry_run"
		result.Duration = time.Since(start)
		return result
	}

	switch step.Type {
	case "assess":
		ds := e.getInputDataset(step)
		if ds == nil {
			result.Error = fmt.Errorf("no input dataset for step %s", step.Name)
			result.Status = "failed"
			result.Duration = time.Since(start)
			return result
		}

		qc := e.buildQualityConfig()
		assessor := quality.NewAssessor(qc)
		report := assessor.Assess(ds)
		result.Report = report
		result.OutputDS = ds
		result.Status = "completed"

		e.LineageTracker.AddTransformNode(step.Name, ds)
		inputName := e.getInputDatasetName(step)
		if inputName != "" {
			columnLineage := lineage.GenerateColumnLineage(ds, ds, step.Name)
			e.LineageTracker.AddEdge(inputName, step.Name, lineage.TransformAssess, step.Name, len(ds.Rows), ds.Schema.ColumnNames(), columnLineage)
		}

	case "clean":
		ds := e.getInputDataset(step)
		if ds == nil {
			result.Error = fmt.Errorf("no input dataset for step %s", step.Name)
			result.Status = "failed"
			result.Duration = time.Since(start)
			return result
		}

		if step.Condition != nil {
			var report *quality.QualityReport
			for _, dep := range step.DependsOn {
				if sr, ok := e.StepResults[dep]; ok && sr.Report != nil {
					report = sr.Report
					break
				}
			}
			if !e.evaluateCondition(step.Condition, ds, report) {
				result.Status = "skipped_condition"
				result.Duration = time.Since(start)
				return result
			}
		}

		missingCfg, outlierCfg, formatCfg, dedupCfg := config.BuildCleaningConfig(e.Cfg, step, ds)
		cleaner := cleaning.NewCleaner(missingCfg, outlierCfg, formatCfg, dedupCfg, e.AuditLog, e.DryRun)
		cleaned := cleaner.Clean(ds)
		result.OutputDS = cleaned
		e.Datasets[step.Name] = cleaned
		result.Status = "completed"

		e.LineageTracker.AddTransformNode(step.Name, cleaned)
		inputName := e.getInputDatasetName(step)
		if inputName != "" {
			columnLineage := lineage.GenerateColumnLineage(ds, cleaned, step.Name)
			e.LineageTracker.AddEdge(inputName, step.Name, lineage.TransformClean, step.Name, len(cleaned.Rows), cleaned.Schema.ColumnNames(), columnLineage)
		}

	case "output":
		ds := e.getInputDataset(step)
		if ds == nil {
			result.Error = fmt.Errorf("no input dataset for step %s", step.Name)
			result.Status = "failed"
			result.Duration = time.Since(start)
			return result
		}

		if err := e.writeOutput(ds); err != nil {
			result.Error = err
			result.Status = "failed"
		} else {
			result.Status = "completed"
		}

		e.LineageTracker.AddOutputNode(step.Name, ds)
		inputName := e.getInputDatasetName(step)
		if inputName != "" {
			columnLineage := lineage.GenerateColumnLineage(ds, ds, step.Name)
			e.LineageTracker.AddEdge(inputName, step.Name, lineage.TransformOutput, step.Name, len(ds.Rows), ds.Schema.ColumnNames(), columnLineage)
		}
	}

	result.Duration = time.Since(start)
	return result
}

func (e *Executor) recordCachedStepLineage(step config.StepConfig, ds *datasource.Dataset) {
	inputName := e.getInputDatasetName(step)
	if ds == nil {
		return
	}

	switch step.Type {
	case "assess":
		e.LineageTracker.AddTransformNode(step.Name, ds)
		if inputName != "" {
			columnLineage := lineage.GenerateColumnLineage(ds, ds, step.Name)
			e.LineageTracker.AddEdge(inputName, step.Name, lineage.TransformAssess, step.Name, len(ds.Rows), ds.Schema.ColumnNames(), columnLineage)
		}
	case "clean":
		e.LineageTracker.AddTransformNode(step.Name, ds)
		if inputName != "" {
			columnLineage := lineage.GenerateColumnLineage(ds, ds, step.Name)
			e.LineageTracker.AddEdge(inputName, step.Name, lineage.TransformClean, step.Name, len(ds.Rows), ds.Schema.ColumnNames(), columnLineage)
		}
	case "output":
		e.LineageTracker.AddOutputNode(step.Name, ds)
		if inputName != "" {
			columnLineage := lineage.GenerateColumnLineage(ds, ds, step.Name)
			e.LineageTracker.AddEdge(inputName, step.Name, lineage.TransformOutput, step.Name, len(ds.Rows), ds.Schema.ColumnNames(), columnLineage)
		}
	}
}

func (e *Executor) getInputDataset(step config.StepConfig) *datasource.Dataset {
	if len(step.DependsOn) > 0 {
		for _, dep := range step.DependsOn {
			if sr, ok := e.StepResults[dep]; ok && sr.OutputDS != nil {
				return sr.OutputDS
			}
			if ds, ok := e.Datasets[dep]; ok {
				return ds
			}
		}
	}

	if ds, ok := e.Datasets["merged"]; ok {
		return ds
	}
	for _, ds := range e.Datasets {
		return ds
	}
	return nil
}

func (e *Executor) getInputDatasetName(step config.StepConfig) string {
	if len(step.DependsOn) > 0 {
		for _, dep := range step.DependsOn {
			if sr, ok := e.StepResults[dep]; ok && sr.OutputDS != nil {
				return dep
			}
			if _, ok := e.Datasets[dep]; ok {
				return dep
			}
		}
	}

	if _, ok := e.Datasets["merged"]; ok {
		return "merged"
	}
	for name := range e.Datasets {
		return name
	}
	return ""
}

func (e *Executor) SaveLineage() (string, error) {
	graph := e.LineageTracker.GetGraph()
	return e.LineageStorage.Save(graph)
}

func (e *Executor) evaluateCondition(cond *config.ConditionConfig, ds *datasource.Dataset, report *quality.QualityReport) bool {
	if report == nil {
		qc := e.buildQualityConfig()
		assessor := quality.NewAssessor(qc)
		report = assessor.Assess(ds)
	}

	switch cond.Field {
	case "dqi":
		val, ok := cond.Value.(float64)
		if !ok {
			if vi, ok := cond.Value.(int); ok {
				val = float64(vi)
			}
		}
		switch cond.Op {
		case ">":
			return report.DQI > val
		case ">=":
			return report.DQI >= val
		case "<":
			return report.DQI < val
		case "<=":
			return report.DQI <= val
		case "==":
			return report.DQI == val
		}
	}
	return true
}

func (e *Executor) buildQualityConfig() *quality.QualityConfig {
	qc := &quality.QualityConfig{
		Weights:           e.Cfg.Quality.Weights,
		PrimaryKey:        e.Cfg.Quality.PrimaryKey,
		UniqueKeys:        e.Cfg.Quality.UniqueKeys,
		RangeChecks:       e.Cfg.Quality.RangeChecks,
		ConsistencyRules:  e.Cfg.Quality.ConsistencyRules,
		ValidityRules:     e.Cfg.Quality.ValidityRules,
		ReferentialChecks: e.Cfg.Quality.ReferentialChecks,
	}
	if e.Cfg.Quality.TimelinessThreshold != nil {
		qc.TimelinessThreshold = e.Cfg.Quality.TimelinessThreshold
	}
	return qc
}

func (e *Executor) writeOutput(ds *datasource.Dataset) error {
	outDir := e.Cfg.Output.Directory
	if outDir == "" {
		outDir = "./output"
	}
	os.MkdirAll(outDir, 0755)

	format := e.Cfg.Output.Format
	if format == "" {
		format = "csv"
	}

	switch format {
	case "csv":
		return writeCSV(ds, filepath.Join(outDir, "output.csv"))
	case "json":
		return writeJSON(ds, filepath.Join(outDir, "output.json"))
	case "parquet":
		return fmt.Errorf("parquet output not yet supported")
	default:
		return writeCSV(ds, filepath.Join(outDir, "output.csv"))
	}
}

func (e *Executor) hasCachedResult(step config.StepConfig) bool {
	if !e.Cfg.Cache.Enabled {
		return false
	}
	cacheFile := e.cachePath(step)
	info, err := os.Stat(cacheFile)
	if err != nil {
		return false
	}

	hashFile := cacheFile + ".hash"
	savedHash, _ := os.ReadFile(hashFile)
	currentHash := e.stepHash(step)
	return string(savedHash) == currentHash && info.Size() > 0
}

func (e *Executor) cacheStepResult(step config.StepConfig) {
	if !e.Cfg.Cache.Enabled {
		return
	}
	cacheFile := e.cachePath(step)
	hashFile := cacheFile + ".hash"

	if sr, ok := e.StepResults[step.Name]; ok && sr.OutputDS != nil {
		os.MkdirAll(filepath.Dir(cacheFile), 0755)
		writeJSON(sr.OutputDS, cacheFile)
		os.WriteFile(hashFile, []byte(e.stepHash(step)), 0644)
	}
}

func (e *Executor) cachePath(step config.StepConfig) string {
	return filepath.Join(e.CacheDir, step.Name+".json")
}

func (e *Executor) stepHash(step config.StepConfig) string {
	data, _ := json.Marshal(step)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func (e *Executor) loadCachedDataset(step config.StepConfig) *datasource.Dataset {
	cacheFile := e.cachePath(step)
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil
	}

	ds, err := datasource.DatasetFromMap(step.Name, records)
	if err != nil {
		return nil
	}
	return ds
}

func topologicalSort(steps []config.StepConfig) ([]string, error) {
	inDegree := make(map[string]int)
	graph := make(map[string][]string)
	stepNames := make(map[string]bool)

	for _, step := range steps {
		stepNames[step.Name] = true
		if _, ok := inDegree[step.Name]; !ok {
			inDegree[step.Name] = 0
		}
		for _, dep := range step.DependsOn {
			graph[dep] = append(graph[dep], step.Name)
			inDegree[step.Name]++
		}
	}

	var queue []string
	for name := range stepNames {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		sorted = append(sorted, name)

		for _, next := range graph[name] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(sorted) != len(steps) {
		return nil, fmt.Errorf("cycle detected in pipeline DAG")
	}

	return sorted, nil
}

func writeCSV(ds *datasource.Dataset, filepath string) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	for i, col := range ds.Schema.Columns {
		if i > 0 {
			f.WriteString(",")
		}
		f.WriteString(col.Name)
	}
	f.WriteString("\n")

	for _, row := range ds.Rows {
		for i := range ds.Schema.Columns {
			if i > 0 {
				f.WriteString(",")
			}
			if i < len(row.Values) {
				val := datasource.FormatCellValue(row.Values[i])
				if needsCSVQuote(val) {
					f.WriteString("\"" + val + "\"")
				} else {
					f.WriteString(val)
				}
			}
		}
		f.WriteString("\n")
	}

	return nil
}

func needsCSVQuote(s string) bool {
	for _, c := range s {
		if c == ',' || c == '"' || c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

func writeJSON(ds *datasource.Dataset, filepath string) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	records := make([]map[string]interface{}, len(ds.Rows))
	for i, row := range ds.Rows {
		record := make(map[string]interface{})
		for j, col := range ds.Schema.Columns {
			if j < len(row.Values) {
				record[col.Name] = datasource.CellValueToJSON(row.Values[j])
			} else {
				record[col.Name] = nil
			}
		}
		records[i] = record
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(records)
}
