package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/data-cleaner/internal/audit"
	"github.com/data-cleaner/internal/cleaning"
	"github.com/data-cleaner/internal/config"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/incremental"
	"github.com/data-cleaner/internal/lineage"
	"github.com/data-cleaner/internal/monitor"
	"github.com/data-cleaner/internal/pipeline"
	"github.com/data-cleaner/internal/quality"
	"github.com/data-cleaner/internal/recommend"
	"github.com/data-cleaner/internal/report"
	"github.com/data-cleaner/internal/templates"
	"github.com/spf13/cobra"
)

var (
	cfgFile          string
	inputFile        string
	outputDir        string
	outputFmt        string
	verbose          bool
	dryRun           bool
	incrementalOn    bool
	exportTpl        bool
	lineageJSON      bool
	lineageDOT       bool
	lineageDepth     int
	lineageColumn    string
	lineageSource    string
	recommendApply   bool
	recommendYes     bool
	recommendYAML    bool
	recommendMinConf int
	recommendFocus   string
	monitorDaemon    bool
	monitorLastN     int
	monitorTaskName  string
)

var rootCmd = &cobra.Command{
	Use:   "data-cleaner",
	Short: "Multi-source heterogeneous data quality assessment and auto-cleaning tool",
	Long: `data-cleaner is a CLI tool for data quality assessment and automatic cleaning.
It supports multiple data sources (CSV, JSON, Excel, Parquet, PostgreSQL, MySQL, API),
six-dimension quality assessment, rule-based validation, anomaly detection,
and configurable cleaning pipelines.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "YAML configuration file")
	rootCmd.PersistentFlags().StringVarP(&inputFile, "input", "i", "", "Input file (overrides config data source)")
	rootCmd.PersistentFlags().StringVarP(&outputDir, "output", "o", "", "Output directory")
	rootCmd.PersistentFlags().StringVarP(&outputFmt, "format", "f", "", "Output format (csv/json/parquet)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose logging")
	rootCmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "n", false, "Dry run (preview only, no actual changes)")
	rootCmd.PersistentFlags().BoolVarP(&incrementalOn, "incremental", "", false, "Enable incremental cleaning mode")
}

func loadConfigOrDie() *config.Config {
	if cfgFile == "" {
		fmt.Fprintln(os.Stderr, "Error: --config flag is required")
		os.Exit(1)
	}
	cfg, err := config.LoadConfig(cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	config.ApplyTemplates(cfg)
	return cfg
}

func loadDataset(cfg *config.Config) *datasource.Dataset {
	if inputFile != "" {
		ds, err := loadFromFile(inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading input: %v\n", err)
			os.Exit(1)
		}
		return ds
	}

	exec := pipeline.NewExecutor(cfg, nil, dryRun)
	if err := exec.LoadSources(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading sources: %v\n", err)
		os.Exit(1)
	}

	if ds, ok := exec.Datasets["merged"]; ok {
		return ds
	}
	for _, ds := range exec.Datasets {
		return ds
	}
	return nil
}

func loadFromFile(path string) (*datasource.Dataset, error) {
	ext := getExtension(path)
	switch ext {
	case ".csv":
		return datasource.ReadCSV(path)
	case ".json":
		return datasource.ReadJSON(path)
	case ".xlsx":
		return datasource.ReadExcel(path, "")
	case ".parquet":
		return datasource.ReadParquet(path)
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}
}

func getExtension(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' || path[i] == '\\' {
			break
		}
	}
	return ""
}

var assessCmd = &cobra.Command{
	Use:   "assess",
	Short: "Assess data quality without cleaning",
	Long:  "Run quality assessment on the specified data source and output a quality report.",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		ds := loadDataset(cfg)

		qc := buildQualityConfig(cfg)
		assessor := quality.NewAssessor(qc)
		rpt := assessor.Assess(ds)

		report.PrintQualityReport(rpt)

		out := outputDir
		if out == "" {
			out = cfg.Output.Directory
		}
		if out == "" {
			out = "./output"
		}

		report.WriteQualityReport(rpt, out)
		if verbose {
			fmt.Printf("\nQuality report written to %s/quality_report.json\n", out)
		}
	},
}

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Execute data cleaning pipeline",
	Long:  "Run the full cleaning pipeline: load data, assess quality, apply cleaning strategies, and output results.",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()

		auditPath := cfg.Output.AuditLog
		var auditLog *audit.Logger
		if auditPath != "" {
			var err error
			auditLog, err = audit.NewLogger(auditPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not create audit log: %v\n", err)
				auditLog = audit.NewMemoryLogger()
			}
		} else {
			auditLog = audit.NewMemoryLogger()
		}
		defer auditLog.Close()

		if incrementalOn {
			runIncrementalClean(cfg, auditLog)
		} else {
			runFullClean(cfg, auditLog)
		}
	},
}

func runFullClean(cfg *config.Config, auditLog *audit.Logger) {
	exec := pipeline.NewExecutor(cfg, auditLog, dryRun)

	if err := exec.LoadSources(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading sources: %v\n", err)
		os.Exit(1)
	}

	var mainDS *datasource.Dataset
	if ds, ok := exec.Datasets["merged"]; ok {
		mainDS = ds
	} else {
		for _, ds := range exec.Datasets {
			mainDS = ds
			break
		}
	}

	if mainDS == nil {
		fmt.Fprintln(os.Stderr, "Error: no data loaded")
		os.Exit(1)
	}

	qc := buildQualityConfig(cfg)
	assessor := quality.NewAssessor(qc)
	beforeReport := assessor.Assess(mainDS)

	if verbose {
		fmt.Println("Before cleaning:")
		report.PrintQualityReport(beforeReport)
	}

	if err := exec.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Pipeline error: %v\n", err)
		os.Exit(1)
	}

	var afterDS *datasource.Dataset
	for _, step := range cfg.Pipeline.Steps {
		if sr, ok := exec.StepResults[step.Name]; ok && sr.OutputDS != nil {
			afterDS = sr.OutputDS
		}
	}

	if afterDS == nil {
		afterDS = mainDS
	}

	afterReport := assessor.Assess(afterDS)

	out := outputDir
	if out == "" {
		out = cfg.Output.Directory
	}
	if out == "" {
		out = "./output"
	}

	comp := report.GenerateComparison(beforeReport, afterReport, mainDS, afterDS, auditLog.Entries())
	comp.PrintTerminal()

	if cfg.Output.HTML {
		comp.WriteHTML(out)
		if verbose {
			fmt.Printf("\nHTML report written to %s/report.html\n", out)
		}
	}

	report.WriteQualityReport(afterReport, out)

	if _, err := exec.SaveLineage(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save lineage: %v\n", err)
	} else if verbose {
		fmt.Println("\nLineage graph saved successfully")
	}

	if verbose {
		fmt.Printf("\nPipeline completed. Results in %s\n", out)
	}
}

func runIncrementalClean(cfg *config.Config, auditLog *audit.Logger) {
	cacheDir := cfg.Cache.Directory
	if cacheDir == "" {
		cacheDir = ".data-cleaner-cache/"
	}

	configHash := config.ComputeConfigHash(cfg)

	proc := incremental.NewIncrementalProcessor(cacheDir, configHash, auditLog, dryRun)

	exec := pipeline.NewExecutor(cfg, auditLog, dryRun)
	if err := exec.LoadSources(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading sources: %v\n", err)
		os.Exit(1)
	}

	var mainDS *datasource.Dataset
	if ds, ok := exec.Datasets["merged"]; ok {
		mainDS = ds
	} else {
		for _, ds := range exec.Datasets {
			mainDS = ds
			break
		}
	}

	if mainDS == nil {
		fmt.Fprintln(os.Stderr, "Error: no data loaded")
		os.Exit(1)
	}

	qc := buildQualityConfig(cfg)
	assessor := quality.NewAssessor(qc)

	assessFn := func(ds *datasource.Dataset) *quality.QualityReport {
		return assessor.Assess(ds)
	}

	cleanFn := func(ds *datasource.Dataset) *datasource.Dataset {
		beforeReport := assessor.Assess(ds)
		if verbose {
			fmt.Println("Before cleaning (incremental subset):")
			report.PrintQualityReport(beforeReport)
		}

		incExec := pipeline.NewExecutor(cfg, auditLog, dryRun)
		incExec.Datasets["merged"] = ds
		for _, src := range cfg.Sources {
			incExec.Datasets[src.Name] = ds
		}

		if err := incExec.Execute(); err != nil {
			fmt.Fprintf(os.Stderr, "Pipeline error (incremental): %v\n", err)
			return ds
		}

		var resultDS *datasource.Dataset
		for _, step := range cfg.Pipeline.Steps {
			if sr, ok := incExec.StepResults[step.Name]; ok && sr.OutputDS != nil {
				resultDS = sr.OutputDS
			}
		}
		if resultDS == nil {
			return ds
		}
		return resultDS
	}

	resultDS, stats, diff := proc.ProcessIncremental(mainDS, cfg, assessFn, cleanFn)

	beforeReport := assessor.Assess(mainDS)
	afterReport := assessor.Assess(resultDS)

	out := outputDir
	if out == "" {
		out = cfg.Output.Directory
	}
	if out == "" {
		out = "./output"
	}

	comp := report.GenerateComparison(beforeReport, afterReport, mainDS, resultDS, auditLog.Entries())
	comp.PrintTerminal()

	incremental.PrintIncrementalReport(stats, diff)

	if cfg.Output.HTML {
		comp.WriteHTML(out)
		if verbose {
			fmt.Printf("\nHTML report written to %s/report.html\n", out)
		}
	}

	report.WriteQualityReport(afterReport, out)

	if _, err := exec.SaveLineage(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save lineage: %v\n", err)
	} else if verbose {
		fmt.Println("\nLineage graph saved successfully")
	}

	if verbose {
		fmt.Printf("\nPipeline completed (incremental). Results in %s\n", out)
	}
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate YAML configuration syntax",
	Long:  "Validate the configuration file for syntax errors, invalid rules, and DAG cycles.",
	Run: func(cmd *cobra.Command, args []string) {
		if cfgFile == "" {
			fmt.Fprintln(os.Stderr, "Error: --config flag is required")
			os.Exit(1)
		}

		cfg, err := config.LoadConfig(cfgFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ Configuration parse error: %v\n", err)
			os.Exit(1)
		}

		config.ApplyTemplates(cfg)

		fmt.Println("✓ Configuration parsed successfully")

		errs := config.ValidateConfig(cfg)
		if len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "\nValidation errors:")
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "  ✗ %v\n", e)
			}
			os.Exit(1)
		}

		fmt.Println("✓ All validation checks passed")
		fmt.Printf("  Sources: %d\n", len(cfg.Sources))
		fmt.Printf("  Pipeline steps: %d\n", len(cfg.Pipeline.Steps))
		fmt.Printf("  Quality rules: %d builtin, %d DSL\n",
			len(cfg.Rules.Builtin), len(cfg.Rules.DSL))
		if len(cfg.Templates) > 0 {
			fmt.Printf("  Templates: %v\n", cfg.Templates)
		}
	},
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate quality report",
	Long:  "Generate a quality assessment report from previously cleaned data or a specified data source.",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		ds := loadDataset(cfg)

		qc := buildQualityConfig(cfg)
		assessor := quality.NewAssessor(qc)
		rpt := assessor.Assess(ds)

		report.PrintQualityReport(rpt)

		out := outputDir
		if out == "" {
			out = cfg.Output.Directory
		}
		if out == "" {
			out = "./output"
		}

		report.WriteQualityReport(rpt, out)

		if cfg.Output.HTML {
			generateStandaloneHTML(rpt, out)
		}

		if verbose {
			fmt.Printf("\nReport written to %s\n", out)
		}
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate configuration template",
	Long:  "Generate a YAML configuration template file with all available options.",
	Run: func(cmd *cobra.Command, args []string) {
		template := config.GenerateTemplate()

		outPath := "data-cleaner.yaml"
		if len(args) > 0 {
			outPath = args[0]
		}

		if err := os.WriteFile(outPath, []byte(template), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing template: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Configuration template written to %s\n", outPath)
		fmt.Println("Edit the file to configure your data cleaning pipeline.")
	},
}

var templatesCmd = &cobra.Command{
	Use:   "templates [name]",
	Short: "List or show industry rule templates",
	Long:  `List available industry rule templates, or show details for a specific template.
Use --export <name> to export a template as YAML.`,
	Run: func(cmd *cobra.Command, args []string) {
		if exportTpl {
			if len(args) == 0 {
				fmt.Fprintln(os.Stderr, "Error: template name is required with --export")
				os.Exit(1)
			}
			yaml, err := templates.ExportTemplateYAML(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(yaml)
			return
		}

		if len(args) == 0 {
			templates.PrintTemplateList()
			return
		}

		templates.PrintTemplateDetail(args[0])
	},
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

func generateStandaloneHTML(rpt *quality.QualityReport, outDir string) {
	os.MkdirAll(outDir, 0755)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>Quality Report - %s</title>
<style>
body{font-family:sans-serif;margin:20px;background:#f5f5f5}
h1{color:#333;border-bottom:2px solid #4a9eff;padding-bottom:10px}
table{border-collapse:collapse;width:100%%;margin:10px 0;background:white;box-shadow:0 1px 3px rgba(0,0,0,0.1)}
th,td{border:1px solid #ddd;padding:10px 14px;text-align:left}
th{background:#4a9eff;color:white}
</style></head><body>
<h1>Quality Report: %s</h1>
<p>Rows: %d | Columns: %d | DQI: %.1f</p>
<table><tr><th>Dimension</th><th>Score</th><th>Details</th></tr>`, rpt.DatasetName, rpt.DatasetName, rpt.TotalRows, rpt.TotalColumns, rpt.DQI)

	for _, d := range rpt.Dimensions {
		html += fmt.Sprintf("<tr><td>%s</td><td>%.1f</td><td>%s</td></tr>", d.Dimension, d.Score, d.Details)
	}
	html += "</table></body></html>"

	os.WriteFile(outDir+"/report.html", []byte(html), 0644)
}

func getLineageStorage() *lineage.LineageStorage {
	cacheDir := ".data-cleaner-cache/"
	if cfgFile != "" {
		cfg, err := config.LoadConfig(cfgFile)
		if err == nil && cfg.Cache.Directory != "" {
			cacheDir = cfg.Cache.Directory
		}
	}
	return lineage.NewLineageStorage(cacheDir, 10)
}

var lineageCmd = &cobra.Command{
	Use:   "lineage",
	Short: "Data lineage tracking and analysis",
	Long:  "Commands for tracking and analyzing data lineage across pipeline executions.",
}

var lineageShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show lineage graph as tree",
	Long:  "Display the most recent lineage graph as an ASCII tree.",
	Run: func(cmd *cobra.Command, args []string) {
		storage := getLineageStorage()
		graph, err := storage.LoadLatest()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if lineageJSON {
			jsonStr, err := lineage.ToJSON(graph, true)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(jsonStr)
			return
		}

		if lineageDOT {
			fmt.Println(lineage.ToDOT(graph))
			return
		}

		fmt.Println(lineage.PrintTree(graph, lineageDepth))
	},
}

var lineageTraceCmd = &cobra.Command{
	Use:   "trace",
	Short: "Trace column lineage",
	Long:  "Trace the origin and transformation history of a specific column.",
	Run: func(cmd *cobra.Command, args []string) {
		if lineageColumn == "" {
			fmt.Fprintln(os.Stderr, "Error: --column flag is required")
			os.Exit(1)
		}

		storage := getLineageStorage()
		graph, err := storage.LoadLatest()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		q := lineage.NewGraphQuery(graph)
		outputs := q.GetOutputNodes()
		if len(outputs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: no output nodes found in lineage graph")
			os.Exit(1)
		}

		results, err := q.TraceColumn(lineageColumn, outputs[0].Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if lineageJSON {
			data, _ := json.MarshalIndent(results, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Println(lineage.PrintColumnTrace(results))
	},
}

var lineageImpactCmd = &cobra.Command{
	Use:   "impact",
	Short: "Analyze source impact",
	Long:  "Analyze which downstream nodes and columns are affected by a source change.",
	Run: func(cmd *cobra.Command, args []string) {
		if lineageSource == "" {
			fmt.Fprintln(os.Stderr, "Error: --source flag is required")
			os.Exit(1)
		}

		storage := getLineageStorage()
		graph, err := storage.LoadLatest()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		q := lineage.NewGraphQuery(graph)

		schemaChanges := []lineage.SchemaChange{
			{Column: "*", ChangeType: "schema_change", Description: "Schema modification detected"},
		}

		result, err := q.ImpactAnalysis(lineageSource, schemaChanges)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if lineageJSON {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Println(lineage.PrintImpactAnalysis(result))
	},
}

var lineageHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "List lineage snapshots",
	Long:  "List all stored lineage snapshots with metadata.",
	Run: func(cmd *cobra.Command, args []string) {
		storage := getLineageStorage()
		snapshots, err := storage.ListSnapshots()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if lineageJSON {
			data, _ := json.MarshalIndent(snapshots, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Println(lineage.PrintHistory(snapshots))
	},
}

var lineageDiffCmd = &cobra.Command{
	Use:   "diff <snapshot1> <snapshot2>",
	Short: "Compare two lineage snapshots",
	Long:  "Show differences between two lineage graph snapshots.",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		storage := getLineageStorage()
		graph1, err := storage.Load(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading snapshot1: %v\n", err)
			os.Exit(1)
		}

		graph2, err := storage.Load(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading snapshot2: %v\n", err)
			os.Exit(1)
		}

		diff := lineage.CompareGraphs(graph1, graph2)

		if lineageJSON {
			data, _ := lineage.DiffToJSON(diff, true)
			fmt.Println(data)
			return
		}

		fmt.Println(lineage.PrintDiff(diff))
	},
}

var recommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Generate data quality rule recommendations",
	Long: `Analyze dataset characteristics and generate recommended quality rules based on
statistical features, pattern detection, and column relationships.

Examples:
  data-cleaner recommend -c config.yaml
  data-cleaner recommend -c config.yaml --apply
  data-cleaner recommend -c config.yaml --min-confidence 80
  data-cleaner recommend -c config.yaml --focus email,phone
  data-cleaner recommend -c config.yaml --yaml`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		ds := loadDataset(cfg)

		if ds == nil {
			fmt.Fprintln(os.Stderr, "Error: no data loaded")
			os.Exit(1)
		}

		var focusCols []string
		if recommendFocus != "" {
			focusCols = strings.Split(recommendFocus, ",")
			for i := range focusCols {
				focusCols[i] = strings.TrimSpace(focusCols[i])
			}
		}

		recCfg := &recommend.RecommendConfig{
			MinConfidence: recommendMinConf,
			FocusColumns:  focusCols,
			ApplyToConfig: recommendApply,
			YesToAll:      recommendYes,
			OutputYAML:    recommendYAML,
		}

		engine := recommend.NewRecommendEngine(recCfg)
		analysisResult, recommendations := engine.AnalyzeAndRecommend(ds, cfg)

		if recommendYAML {
			yamlStr, err := recommend.ToYAML(recommendations)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error generating YAML: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(yamlStr)
		} else {
			recommend.PrintRecommendations(recommendations, analysisResult)
			recommend.PrintChangeSummary(recommendations)
		}

		if recommendApply {
			if err := recommend.ApplyToConfig(cfgFile, recommendations, recommendYes); err != nil {
				fmt.Fprintf(os.Stderr, "Error applying recommendations: %v\n", err)
				os.Exit(1)
			}
		}
	},
}

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Data quality monitoring and alerting",
	Long:  `Start, stop, and manage data quality monitoring tasks with cron-based scheduling and alert notifications.`,
}

var monitorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the monitor daemon",
	Long:  `Start the monitoring daemon process. By default runs in foreground; use -d for background mode.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		monitor.SetConfigPath(cfgFile)

		if !cfg.Monitor.Enabled {
			fmt.Fprintln(os.Stderr, "Monitor is not enabled in config. Set monitor.enabled: true")
			os.Exit(1)
		}

		if monitorDaemon {
			if err := monitor.RunDaemon(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Error starting daemon: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := monitor.RunForeground(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "Error running monitor: %v\n", err)
				os.Exit(1)
			}
		}
	},
}

var monitorStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the background monitor daemon",
	Long:  `Send SIGTERM to the background monitor daemon process.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		if err := monitor.StopDaemon(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error stopping daemon: %v\n", err)
			os.Exit(1)
		}
	},
}

var monitorRunCmd = &cobra.Command{
	Use:   "run <task_name>",
	Short: "Manually trigger a single monitoring scan",
	Long:  `Run a single quality scan for the specified monitor task immediately, without waiting for the cron schedule.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		taskName := args[0]

		if err := monitor.RunSingleTask(cfg, taskName); err != nil {
			fmt.Fprintf(os.Stderr, "Error running task '%s': %v\n", taskName, err)
			os.Exit(1)
		}
	},
}

var monitorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show monitor task status",
	Long:  `Display the current status of all monitoring tasks including last run time, next scheduled run, DQI score, and active alert count.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		scheduler := monitor.NewScheduler(cfg, monitor.CacheBaseDir(cfg))
		monitor.PrintStatus(cfg, scheduler)
	},
}

var monitorAlertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "Show active alerts",
	Long:  `Display all currently active alerts (FIRING and PENDING states).`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		scheduler := monitor.NewScheduler(cfg, monitor.CacheBaseDir(cfg))
		monitor.PrintAlerts(scheduler)
	},
}

var monitorHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show scan history for a task",
	Long:  `Display the DQI trend for recent scans of a specified monitoring task.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfigOrDie()
		if monitorTaskName == "" {
			fmt.Fprintln(os.Stderr, "Error: --task flag is required")
			os.Exit(1)
		}
		scheduler := monitor.NewScheduler(cfg, monitor.CacheBaseDir(cfg))
		monitor.PrintHistory(cfg, scheduler, monitorTaskName, monitorLastN)
	},
}

func init() {
	rootCmd.AddCommand(assessCmd)
	rootCmd.AddCommand(cleanCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(templatesCmd)
	rootCmd.AddCommand(lineageCmd)
	rootCmd.AddCommand(recommendCmd)
	rootCmd.AddCommand(monitorCmd)

	lineageCmd.AddCommand(lineageShowCmd)
	lineageCmd.AddCommand(lineageTraceCmd)
	lineageCmd.AddCommand(lineageImpactCmd)
	lineageCmd.AddCommand(lineageHistoryCmd)
	lineageCmd.AddCommand(lineageDiffCmd)

	monitorCmd.AddCommand(monitorStartCmd)
	monitorCmd.AddCommand(monitorStopCmd)
	monitorCmd.AddCommand(monitorRunCmd)
	monitorCmd.AddCommand(monitorStatusCmd)
	monitorCmd.AddCommand(monitorAlertsCmd)
	monitorCmd.AddCommand(monitorHistoryCmd)

	lineageShowCmd.Flags().BoolVar(&lineageJSON, "json", false, "Output in JSON format")
	lineageShowCmd.Flags().BoolVar(&lineageDOT, "dot", false, "Output in Graphviz DOT format")
	lineageShowCmd.Flags().IntVar(&lineageDepth, "depth", 0, "Maximum display depth (0 = unlimited)")

	lineageTraceCmd.Flags().StringVar(&lineageColumn, "column", "", "Column name to trace")
	lineageTraceCmd.Flags().BoolVar(&lineageJSON, "json", false, "Output in JSON format")

	lineageImpactCmd.Flags().StringVar(&lineageSource, "source", "", "Source node name for impact analysis")
	lineageImpactCmd.Flags().BoolVar(&lineageJSON, "json", false, "Output in JSON format")

	lineageHistoryCmd.Flags().BoolVar(&lineageJSON, "json", false, "Output in JSON format")

	lineageDiffCmd.Flags().BoolVar(&lineageJSON, "json", false, "Output in JSON format")

	templatesCmd.Flags().BoolVar(&exportTpl, "export", false, "Export template as YAML to stdout")

	recommendCmd.Flags().BoolVar(&recommendApply, "apply", false, "Apply recommended rules to config file")
	recommendCmd.Flags().BoolVar(&recommendYes, "yes", false, "Skip confirmation when applying rules")
	recommendCmd.Flags().BoolVar(&recommendYAML, "yaml", false, "Output recommendations in YAML format")
	recommendCmd.Flags().IntVar(&recommendMinConf, "min-confidence", 70, "Minimum confidence threshold (0-100)")
	recommendCmd.Flags().StringVar(&recommendFocus, "focus", "", "Only analyze specified columns (comma-separated)")

	monitorStartCmd.Flags().BoolVarP(&monitorDaemon, "daemon", "d", false, "Run as daemon in background")
	monitorHistoryCmd.Flags().StringVar(&monitorTaskName, "task", "", "Task name")
	monitorHistoryCmd.Flags().IntVar(&monitorLastN, "last", 10, "Number of recent scans to show")

	_ = cleaning.MissingMean
	_ = cleaning.OutlierWinsorize
}
