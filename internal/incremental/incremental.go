package incremental

import (
	"fmt"
	"os"
	"time"

	"github.com/data-cleaner/internal/audit"
	"github.com/data-cleaner/internal/config"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/quality"
)

type IncrementalStats struct {
	IncrementalRows    int           `json:"incremental_rows"`
	SkippedRows        int           `json:"skipped_rows"`
	AddedRows          int           `json:"added_rows"`
	ModifiedRows       int           `json:"modified_rows"`
	DeletedRows        int           `json:"deleted_rows"`
	IncrementalTime    time.Duration `json:"incremental_time"`
	EstimatedFullTime  time.Duration `json:"estimated_full_time"`
	FallbackToFull     bool          `json:"fallback_to_full"`
	FingerprintValid   bool          `json:"fingerprint_valid"`
	ConfigHashChanged  bool          `json:"config_hash_changed"`
}

type IncrementalProcessor struct {
	CacheDir    string
	ConfigHash  string
	AuditLog    *audit.Logger
	DryRun      bool
}

func NewIncrementalProcessor(cacheDir, configHash string, auditLog *audit.Logger, dryRun bool) *IncrementalProcessor {
	return &IncrementalProcessor{
		CacheDir:   cacheDir,
		ConfigHash: configHash,
		AuditLog:   auditLog,
		DryRun:     dryRun,
	}
}

func (p *IncrementalProcessor) ProcessIncremental(
	ds *datasource.Dataset,
	cfg *config.Config,
	assessFn func(*datasource.Dataset) *quality.QualityReport,
	cleanFn func(*datasource.Dataset) *datasource.Dataset,
) (*datasource.Dataset, *IncrementalStats, *DiffResult) {
	stats := &IncrementalStats{}

	prevState, err := LoadIncrementalState(p.CacheDir)
	if err != nil || prevState == nil {
		stats.FallbackToFull = true
		stats.FingerprintValid = false
		return p.processFull(ds, cfg, assessFn, cleanFn, stats)
	}

	if !IsFingerprintValid(prevState, p.ConfigHash) {
		stats.FallbackToFull = true
		stats.FingerprintValid = false
		if prevState.Fingerprint.ConfigHash != p.ConfigHash {
			stats.ConfigHashChanged = true
		}
		return p.processFull(ds, cfg, assessFn, cleanFn, stats)
	}

	stats.FingerprintValid = true

	currentFP := ComputeFingerprint(ds, p.ConfigHash)
	diff := DiffFingerprints(prevState.Fingerprint, currentFP)

	stats.AddedRows = len(diff.AddedRows)
	stats.ModifiedRows = len(diff.ModifiedRows)
	stats.DeletedRows = len(diff.DeletedRows)

	changedRowIndices := make(map[int]bool)
	for _, idx := range diff.AddedRows {
		changedRowIndices[idx] = true
	}
	for _, idx := range diff.ModifiedRows {
		changedRowIndices[idx] = true
	}

	deletedSet := make(map[int]bool)
	for _, idx := range diff.DeletedRows {
		deletedSet[idx] = true
	}

	if len(changedRowIndices) == 0 && len(deletedSet) == 0 {
		stats.IncrementalRows = 0
		stats.SkippedRows = len(ds.Rows)
		reconstructed := ReconstructCleanedRows(prevState)
		if reconstructed != nil {
			return reconstructed, stats, &diff
		}
		return p.processFull(ds, cfg, assessFn, cleanFn, stats)
	}

	start := time.Now()

	var changedRows []datasource.Row
	changedOriginalIndices := []int{}
	for i, row := range ds.Rows {
		if changedRowIndices[i] {
			changedRows = append(changedRows, row)
			changedOriginalIndices = append(changedOriginalIndices, i)
		}
	}

	changedDS := &datasource.Dataset{
		Name:   ds.Name + "_incremental",
		Schema: ds.Schema,
		Rows:   changedRows,
	}

	assessFn(changedDS)
	cleanedChangedDS := cleanFn(changedDS)

	reconstructed := ReconstructCleanedRows(prevState)
	if reconstructed == nil {
		return p.processFull(ds, cfg, assessFn, cleanFn, stats)
	}

	resultDS := mergeIncrementalResult(reconstructed, cleanedChangedDS, changedOriginalIndices, deletedSet, ds)

	stats.IncrementalRows = len(changedRowIndices)
	stats.SkippedRows = len(diff.UnchangedRows)
	stats.IncrementalTime = time.Since(start)

	if stats.IncrementalRows > 0 {
		perRow := stats.IncrementalTime / time.Duration(stats.IncrementalRows)
		stats.EstimatedFullTime = perRow * time.Duration(len(ds.Rows))
	}

	if err := SaveFingerprint(p.CacheDir, ds, p.ConfigHash, resultDS); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save incremental state: %v\n", err)
	}

	return resultDS, stats, &diff
}

func (p *IncrementalProcessor) processFull(
	ds *datasource.Dataset,
	cfg *config.Config,
	assessFn func(*datasource.Dataset) *quality.QualityReport,
	cleanFn func(*datasource.Dataset) *datasource.Dataset,
	stats *IncrementalStats,
) (*datasource.Dataset, *IncrementalStats, *DiffResult) {
	start := time.Now()

	assessFn(ds)
	cleanedDS := cleanFn(ds)

	stats.IncrementalRows = len(ds.Rows)
	stats.SkippedRows = 0
	stats.IncrementalTime = time.Since(start)
	stats.EstimatedFullTime = stats.IncrementalTime

	if err := SaveFingerprint(p.CacheDir, ds, p.ConfigHash, cleanedDS); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save incremental state: %v\n", err)
	}

	return cleanedDS, stats, nil
}

func mergeIncrementalResult(
	prevCleaned *datasource.Dataset,
	cleanedChanged *datasource.Dataset,
	changedIndices []int,
	deletedSet map[int]bool,
	currentDS *datasource.Dataset,
) *datasource.Dataset {
	result := &datasource.Dataset{
		Name:   prevCleaned.Name,
		Schema: currentDS.Schema,
		Rows:   make([]datasource.Row, 0, len(currentDS.Rows)),
	}

	cleanedMap := make(map[int]datasource.Row)
	for i, origIdx := range changedIndices {
		if i < len(cleanedChanged.Rows) {
			cleanedMap[origIdx] = cleanedChanged.Rows[i]
		}
	}

	for i := 0; i < len(currentDS.Rows); i++ {
		if deletedSet[i] {
			continue
		}

		if cleanedRow, ok := cleanedMap[i]; ok {
			result.Rows = append(result.Rows, cleanedRow)
		} else if i < len(prevCleaned.Rows) {
			result.Rows = append(result.Rows, prevCleaned.Rows[i])
		} else {
			result.Rows = append(result.Rows, currentDS.Rows[i])
		}
	}

	return result
}

func PrintIncrementalReport(stats *IncrementalStats, diff *DiffResult) {
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│ Incremental Processing Report                               │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	if stats.FallbackToFull {
		reason := "no previous fingerprint found"
		if stats.ConfigHashChanged {
			reason = "configuration changed, fingerprint invalidated"
		}
		fmt.Printf("  Mode: FULL (fallback) - %s\n", reason)
	} else {
		fmt.Println("  Mode: INCREMENTAL")
	}

	fmt.Println("\n┌──────────────────────────┬──────────┐")
	fmt.Println("│ Metric                   │ Value    │")
	fmt.Println("├──────────────────────────┼──────────┤")

	if diff != nil {
		fmt.Printf("│ Total rows (before)      │ %8d │\n", diff.TotalBefore)
		fmt.Printf("│ Total rows (after)       │ %8d │\n", diff.TotalAfter)
		fmt.Printf("│ Added rows               │ %8d │\n", stats.AddedRows)
		fmt.Printf("│ Modified rows            │ %8d │\n", stats.ModifiedRows)
		fmt.Printf("│ Deleted rows             │ %8d │\n", stats.DeletedRows)
	}
	fmt.Printf("│ Processed (incremental)  │ %8d │\n", stats.IncrementalRows)
	fmt.Printf("│ Skipped (unchanged)      │ %8d │\n", stats.SkippedRows)
	fmt.Println("├──────────────────────────┼──────────┤")
	fmt.Printf("│ Incremental time         │ %8s │\n", stats.IncrementalTime.Round(time.Millisecond))
	fmt.Printf("│ Estimated full time      │ %8s │\n", stats.EstimatedFullTime.Round(time.Millisecond))

	if stats.EstimatedFullTime > 0 && stats.IncrementalTime > 0 {
		savings := (1 - float64(stats.IncrementalTime)/float64(stats.EstimatedFullTime)) * 100
		if savings < 0 {
			savings = 0
		}
		fmt.Printf("│ Time saved               │ %7.1f%% │\n", savings)
	}
	fmt.Println("└──────────────────────────┴──────────┘")
}
