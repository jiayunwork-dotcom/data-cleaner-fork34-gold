package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/data-cleaner/internal/cleaning"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/quality"
	"github.com/data-cleaner/internal/rules"
	"github.com/data-cleaner/internal/rules/dsl"
	"github.com/data-cleaner/internal/templates"
)

type Config struct {
	Name      string         `yaml:"name"`
	Version   string         `yaml:"version"`
	Sources   []SourceConfig `yaml:"sources"`
	Quality   QualityConfig  `yaml:"quality"`
	Rules     RulesConfig    `yaml:"rules"`
	Pipeline  PipelineConfig `yaml:"pipeline"`
	Output    OutputConfig   `yaml:"output"`
	Cache     CacheConfig    `yaml:"cache"`
	Lineage   LineageConfig  `yaml:"lineage"`
	Templates []string       `yaml:"templates"`
	Monitor   MonitorConfig  `yaml:"monitor"`
}

type MonitorConfig struct {
	Enabled         bool              `yaml:"enabled"`
	ConnectionPool  int               `yaml:"connection_pool"`
	AggregateWindow string            `yaml:"aggregate_window"`
	Tasks           []MonitorTask     `yaml:"tasks"`
	Channels        []NotifyChannel   `yaml:"channels"`
	InhibitRules    []InhibitRule     `yaml:"inhibit_rules"`
}

type MonitorTask struct {
	Name     string      `yaml:"name"`
	Source   string      `yaml:"source"`
	Schedule string      `yaml:"schedule"`
	Rules    []AlertRule `yaml:"rules"`
	Channels []string    `yaml:"channels"`
}

type AlertRule struct {
	Name            string              `yaml:"name"`
	Metric          string              `yaml:"metric"`
	Operator        string              `yaml:"operator"`
	Threshold       float64             `yaml:"threshold"`
	ForCount        int                 `yaml:"for_count"`
	Silence         string              `yaml:"silence"`
	Template        string              `yaml:"template"`
	Mode            string              `yaml:"mode"`
	BaselineWindow  int                 `yaml:"baseline_window"`
	BaselineSigma   float64             `yaml:"baseline_sigma"`
	SeasonalPeriod  string              `yaml:"seasonal_period"`
	Labels          map[string]string   `yaml:"labels"`
	Escalation      []EscalationLevel   `yaml:"escalation"`
}

type EscalationLevel struct {
	After    string   `yaml:"after"`
	Channels []string `yaml:"channels"`
}

type InhibitRule struct {
	SourceRule   string   `yaml:"source_rule"`
	TargetRules []string `yaml:"target_rules"`
	EqualLabels []string `yaml:"equal_labels"`
}

type NotifyChannel struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	URL     string `yaml:"url"`
	Timeout string `yaml:"timeout"`
}

type SourceConfig struct {
	Name   string             `yaml:"name"`
	Type   string             `yaml:"type"`
	Path   string             `yaml:"path"`
	Sheet  string             `yaml:"sheet"`
	Database *datasource.DBConfig `yaml:"database"`
	API     *datasource.APIConfig `yaml:"api"`
}

type QualityConfig struct {
	Weights             map[string]float64    `yaml:"weights"`
	TimelinessThreshold *string               `yaml:"timeliness_threshold"`
	ConsistencyRules    []quality.ConsistencyRule `yaml:"consistency_rules"`
	ValidityRules       []quality.ValidityRule    `yaml:"validity_rules"`
	PrimaryKey          []string               `yaml:"primary_key"`
	UniqueKeys          [][]string             `yaml:"unique_keys"`
	RangeChecks         []quality.RangeCheckConfig `yaml:"range_checks"`
	ReferentialChecks   []quality.ReferentialCheck `yaml:"referential_checks"`
}

type RulesConfig struct {
	Builtin    []rules.Rule          `yaml:"builtin"`
	CrossField []rules.Rule          `yaml:"cross_field"`
	CrossTable []rules.CrossTableRule `yaml:"cross_table"`
	DSL        []rules.DSLRule       `yaml:"dsl"`
}

type PipelineConfig struct {
	Steps      []StepConfig  `yaml:"steps"`
	MaxWorkers int           `yaml:"max_workers"`
	ErrorPolicy string       `yaml:"error_policy"`
	RetryCount  int          `yaml:"retry_count"`
}

type StepConfig struct {
	Name      string            `yaml:"name"`
	Type      string            `yaml:"type"`
	DependsOn []string          `yaml:"depends_on"`
	Params    map[string]interface{} `yaml:"params"`
	Condition *ConditionConfig  `yaml:"condition"`
}

type ConditionConfig struct {
	Field   string `yaml:"field"`
	Op      string `yaml:"op"`
	Value   interface{} `yaml:"value"`
}

type OutputConfig struct {
	Directory string `yaml:"directory"`
	Format    string `yaml:"format"`
	Reports   bool   `yaml:"reports"`
	AuditLog  string `yaml:"audit_log"`
	HTML      bool   `yaml:"html"`
}

type CacheConfig struct {
	Directory string `yaml:"directory"`
	Enabled   bool   `yaml:"enabled"`
}

type LineageConfig struct {
	Enabled      bool `yaml:"enabled"`
	HistoryCount int  `yaml:"history_count"`
}

var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func resolveEnvVars(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarRe.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

func LoadConfig(filepath string) (*Config, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

func ComputeConfigHash(cfg *Config) string {
	hashData := struct {
		Rules     RulesConfig    `yaml:"rules"`
		Quality   QualityConfig  `yaml:"quality"`
		Pipeline  PipelineConfig `yaml:"pipeline"`
		Templates []string       `yaml:"templates"`
	}{
		Rules:     cfg.Rules,
		Quality:   cfg.Quality,
		Pipeline:  cfg.Pipeline,
		Templates: cfg.Templates,
	}

	data, err := json.Marshal(hashData)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func ApplyTemplates(cfg *Config) {
	if len(cfg.Templates) == 0 {
		return
	}

	mergedBuiltin, mergedCrossField, mergedRangeChecks, mergedConsistencyRules, mergedValidityRules := templates.MergeTemplates(
		cfg.Templates,
		cfg.Rules.Builtin,
		cfg.Rules.CrossField,
		cfg.Quality.RangeChecks,
		cfg.Quality.ConsistencyRules,
		cfg.Quality.ValidityRules,
	)

	cfg.Rules.Builtin = mergedBuiltin
	cfg.Rules.CrossField = mergedCrossField
	cfg.Quality.RangeChecks = mergedRangeChecks
	cfg.Quality.ConsistencyRules = mergedConsistencyRules
	cfg.Quality.ValidityRules = mergedValidityRules
}

func ValidateConfig(cfg *Config) []error {
	var errs []error

	for _, src := range cfg.Sources {
		switch src.Type {
		case "csv", "json", "excel", "parquet":
			if src.Path == "" {
				errs = append(errs, fmt.Errorf("source '%s': path is required for %s type", src.Name, src.Type))
			}
		case "database":
			if src.Database == nil {
				errs = append(errs, fmt.Errorf("source '%s': database config is required", src.Name))
			}
		case "api":
			if src.API == nil {
				errs = append(errs, fmt.Errorf("source '%s': api config is required", src.Name))
			}
		default:
			errs = append(errs, fmt.Errorf("source '%s': unknown type '%s'", src.Name, src.Type))
		}
	}

	for _, rule := range cfg.Rules.Builtin {
		if rule.Field == "" {
			errs = append(errs, fmt.Errorf("rule '%s': field is required", rule.ID))
		}
	}

	for _, dslRule := range cfg.Rules.DSL {
		if err := dsl.ValidateExpression(dslRule.Expression); err != nil {
			errs = append(errs, fmt.Errorf("DSL rule '%s': %w", dslRule.ID, err))
		}
	}

	if err := validatePipelineDAG(cfg.Pipeline.Steps); err != nil {
		errs = append(errs, err)
	}

	for _, tplName := range cfg.Templates {
		if _, ok := templates.Get(tplName); !ok {
			errs = append(errs, fmt.Errorf("template '%s' not found", tplName))
		}
	}

	if cfg.Monitor.Enabled {
		errs = append(errs, validateMonitorConfig(cfg)...)
	}

	return errs
}

func validateMonitorConfig(cfg *Config) []error {
	var errs []error

	sourceNames := make(map[string]bool)
	for _, src := range cfg.Sources {
		sourceNames[src.Name] = true
	}

	channelNames := make(map[string]bool)
	for _, ch := range cfg.Monitor.Channels {
		channelNames[ch.Name] = true
		if ch.URL == "" {
			errs = append(errs, fmt.Errorf("monitor channel '%s': url is required", ch.Name))
		}
	}

	validOperators := map[string]bool{"<": true, ">": true, "<=": true, ">=": true, "==": true}
	validMetrics := map[string]bool{
		"dqi": true, "completeness": true, "consistency": true,
		"accuracy": true, "uniqueness": true, "timeliness": true, "validity": true,
	}
	validModes := map[string]bool{"": true, "static": true, "dynamic_baseline": true}
	validSeasonalPeriods := map[string]bool{"": true, "1h": true, "24h": true, "168h": true}

	taskNames := make(map[string]bool)
	ruleGlobalNames := make(map[string]string)
	for _, task := range cfg.Monitor.Tasks {
		if task.Name == "" {
			errs = append(errs, fmt.Errorf("monitor task: name is required"))
			continue
		}
		if taskNames[task.Name] {
			errs = append(errs, fmt.Errorf("monitor task '%s': duplicate name", task.Name))
		}
		taskNames[task.Name] = true

		if !sourceNames[task.Source] {
			errs = append(errs, fmt.Errorf("monitor task '%s': source '%s' not found", task.Name, task.Source))
		}
		if task.Schedule == "" {
			errs = append(errs, fmt.Errorf("monitor task '%s': schedule is required", task.Name))
		}

		for _, rule := range task.Rules {
			if rule.Name == "" {
				errs = append(errs, fmt.Errorf("monitor task '%s': rule name is required", task.Name))
			}
			if _, exists := ruleGlobalNames[rule.Name]; exists {
				errs = append(errs, fmt.Errorf("monitor rule '%s': duplicate rule name across tasks (in task '%s' and '%s')", rule.Name, task.Name, ruleGlobalNames[rule.Name]))
			}
			ruleGlobalNames[rule.Name] = task.Name

			if !validMetrics[rule.Metric] {
				errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': invalid metric '%s'", task.Name, rule.Name, rule.Metric))
			}
			if !validOperators[rule.Operator] {
				errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': invalid operator '%s'", task.Name, rule.Name, rule.Operator))
			}
			if rule.ForCount < 1 {
				errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': for_count must be >= 1", task.Name, rule.Name))
			}
			if !validModes[rule.Mode] {
				errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': invalid mode '%s' (valid: static, dynamic_baseline)", task.Name, rule.Name, rule.Mode))
			}
			if rule.Mode == "dynamic_baseline" {
				if rule.BaselineWindow < 5 && rule.BaselineWindow != 0 {
					errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': baseline_window must be >= 5", task.Name, rule.Name))
				}
				if rule.BaselineSigma <= 0 && rule.BaselineSigma != 0 {
					errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': baseline_sigma must be > 0", task.Name, rule.Name))
				}
				if !validSeasonalPeriods[rule.SeasonalPeriod] {
					errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s': invalid seasonal_period '%s' (valid: 1h, 24h, 168h)", task.Name, rule.Name, rule.SeasonalPeriod))
				}
			}

			for i, esc := range rule.Escalation {
				if esc.After == "" {
					errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s' escalation[%d]: after is required", task.Name, rule.Name, i))
				} else if _, err := time.ParseDuration(esc.After); err != nil {
					errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s' escalation[%d]: invalid duration '%s'", task.Name, rule.Name, i, esc.After))
				}
				for _, chName := range esc.Channels {
					if !channelNames[chName] {
						errs = append(errs, fmt.Errorf("monitor task '%s' rule '%s' escalation[%d]: channel '%s' not found", task.Name, rule.Name, i, chName))
					}
				}
			}
		}

		for _, chName := range task.Channels {
			if !channelNames[chName] {
				errs = append(errs, fmt.Errorf("monitor task '%s': channel '%s' not found", task.Name, chName))
			}
		}
	}

	errs = append(errs, validateInhibitRules(cfg)...)

	return errs
}

func validateInhibitRules(cfg *Config) []error {
	var errs []error

	ruleNames := make(map[string]bool)
	for _, task := range cfg.Monitor.Tasks {
		for _, rule := range task.Rules {
			ruleNames[rule.Name] = true
		}
	}

	for i, ir := range cfg.Monitor.InhibitRules {
		if ir.SourceRule == "" {
			errs = append(errs, fmt.Errorf("inhibit_rules[%d]: source_rule is required", i))
		} else if !ruleNames[ir.SourceRule] {
			errs = append(errs, fmt.Errorf("inhibit_rules[%d]: source_rule '%s' not found", i, ir.SourceRule))
		}
		for _, tr := range ir.TargetRules {
			if !ruleNames[tr] {
				errs = append(errs, fmt.Errorf("inhibit_rules[%d]: target_rule '%s' not found", i, tr))
			}
		}
		if len(ir.EqualLabels) == 0 {
			errs = append(errs, fmt.Errorf("inhibit_rules[%d]: equal_labels is required", i))
		}
	}

	if err := detectInhibitCycles(cfg.Monitor.InhibitRules); err != nil {
		errs = append(errs, err)
	}

	return errs
}

func detectInhibitCycles(rules []InhibitRule) error {
	graph := make(map[string][]string)
	for _, ir := range rules {
		for _, tr := range ir.TargetRules {
			graph[ir.SourceRule] = append(graph[ir.SourceRule], tr)
		}
	}

	visited := make(map[string]int)
	var visit func(string) error

	visit = func(node string) error {
		if visited[node] == 1 {
			return fmt.Errorf("circular dependency detected in inhibit_rules involving rule '%s'", node)
		}
		if visited[node] == 2 {
			return nil
		}
		visited[node] = 1
		for _, neighbor := range graph[node] {
			if err := visit(neighbor); err != nil {
				return err
			}
		}
		visited[node] = 2
		return nil
	}

	for _, ir := range rules {
		if err := visit(ir.SourceRule); err != nil {
			return err
		}
	}
	return nil
}

func validatePipelineDAG(steps []StepConfig) error {
	if len(steps) == 0 {
		return nil
	}

	stepMap := make(map[string]bool)
	for _, step := range steps {
		stepMap[step.Name] = true
	}

	for _, step := range steps {
		for _, dep := range step.DependsOn {
			if !stepMap[dep] {
				return fmt.Errorf("step '%s' depends on unknown step '%s'", step.Name, dep)
			}
		}
	}

	visited := make(map[string]int)
	var visit func(name string) error

	visit = func(name string) error {
		if visited[name] == 1 {
			return fmt.Errorf("cycle detected at step '%s'", name)
		}
		if visited[name] == 2 {
			return nil
		}

		visited[name] = 1
		for _, step := range steps {
			if step.Name == name {
				for _, dep := range step.DependsOn {
					if err := visit(dep); err != nil {
						return err
					}
				}
			}
		}
		visited[name] = 2
		return nil
	}

	for _, step := range steps {
		if err := visit(step.Name); err != nil {
			return err
		}
	}

	return nil
}

func GenerateTemplate() string {
	return `name: data-cleaner-pipeline
version: "1.0"

sources:
  - name: main_data
    type: csv
    path: data/input.csv
  - name: supplementary
    type: json
    path: data/supplementary.json
  - name: db_source
    type: database
    database:
      driver: postgres
      host: localhost
      port: 5432
      user: postgres
      password: ${DB_PASSWORD}
      database: mydb
      table: users
  - name: api_source
    type: api
    api:
      url: "https://api.example.com/data"
      headers:
        Authorization: "Bearer ${API_TOKEN}"
      data_path: "results"
      pagination:
        mode: offset
        offset_param: offset
        limit_param: limit
        page_size: 100
        max_pages: 10

quality:
  weights:
    completeness: 0.2
    consistency: 0.15
    accuracy: 0.2
    uniqueness: 0.15
    timeliness: 0.15
    validity: 0.15
  timeliness_threshold: "720h"
  primary_key: ["id"]
  range_checks:
    - field: age
      min: 0
      max: 150
    - field: longitude
      min: -180
      max: 180
  consistency_rules:
    - type: compare
      field_a: start_date
      field_b: end_date
      expression: "<="
  validity_rules:
    - field: email
      pattern: '^[\\w.]+@[\\w.]+\\.[\\w]+$'

rules:
  builtin:
    - id: not_null_id
      type: not_null
      field: id
      critical: true
    - id: age_range
      type: range
      field: age
      params:
        min: 0
        max: 150
      critical: true
    - id: email_format
      type: regex
      field: email
      params:
        pattern: '^[\\w.]+@[\\w.]+\\.[\\w]+$'
      critical: false
  cross_field:
    - id: date_order
      type: cross_field
      field: start_date
      params:
        field_a: start_date
        field_b: end_date
        operator: "<="
      critical: true
  dsl:
    - id: age_email_check
      expression: "age >= 0 and age <= 150"
      critical: true

pipeline:
  max_workers: 4
  error_policy: continue
  retry_count: 1
  steps:
    - name: assess
      type: assess
    - name: light_clean
      type: clean
      depends_on: [assess]
      condition:
        field: dqi
        op: ">"
        value: 80
      params:
        missing_strategy: median
        outlier_strategy: winsorize
    - name: deep_clean
      type: clean
      depends_on: [assess]
      condition:
        field: dqi
        op: "<="
        value: 80
      params:
        missing_strategy: knn
        outlier_strategy: to_null
    - name: validate
      type: assess
      depends_on: [light_clean, deep_clean]
    - name: output
      type: output
      depends_on: [validate]

output:
  directory: ./output
  format: csv
  reports: true
  audit_log: audit.jsonl
  html: true

cache:
  directory: .data-cleaner-cache/
  enabled: true

monitor:
  enabled: false
  connection_pool: 5
  aggregate_window: 30s
  tasks:
    - name: daily_quality_check
      source: main_data
      schedule: "0 9 * * *"
      rules:
        - name: low_dqi
          metric: dqi
          operator: "<"
          threshold: 80
          for_count: 2
          silence: 1h
        - name: high_null_rate
          metric: completeness
          operator: "<"
          threshold: 90
          for_count: 3
          silence: 30m
      channels: [webhook_default]
  channels:
    - name: webhook_default
      type: webhook
      url: "http://localhost:8080/alert"
      timeout: 10s
`
}

func BuildCleaningConfig(cfg *Config, step StepConfig, ds *datasource.Dataset) (*cleaning.MissingConfig, *cleaning.OutlierConfig, *cleaning.FormatConfig, *cleaning.DedupConfig) {
	missingCfg := &cleaning.MissingConfig{
		Columns: make(map[string]cleaning.MissingStrategy),
	}
	outlierCfg := &cleaning.OutlierConfig{}
	formatCfg := &cleaning.FormatConfig{}
	dedupCfg := &cleaning.DedupConfig{}

	if ms, ok := step.Params["missing_strategy"].(string); ok {
		if ds != nil {
			for _, col := range ds.Schema.Columns {
				missingCfg.Columns[col.Name] = cleaning.MissingStrategy(ms)
			}
		} else {
			for _, col := range cfg.Quality.PrimaryKey {
				missingCfg.Columns[col] = cleaning.MissingStrategy(ms)
			}
		}
	}
	if k, ok := step.Params["knn_k"].(int); ok {
		missingCfg.KNNK = k
	}
	if os, ok := step.Params["outlier_strategy"].(string); ok {
		outlierCfg.Strategy = cleaning.OutlierStrategy(os)
		if ds != nil {
			for _, col := range ds.Schema.Columns {
				if col.DataType == datasource.TypeInt || col.DataType == datasource.TypeFloat {
					outlierCfg.Columns = append(outlierCfg.Columns, col.Name)
				}
			}
		}
	}
	if cols, ok := step.Params["outlier_columns"].([]interface{}); ok {
		for _, c := range cols {
			if s, ok := c.(string); ok {
				outlierCfg.Columns = append(outlierCfg.Columns, s)
			}
		}
	}
	if dedupCols, ok := step.Params["dedup_columns"].([]interface{}); ok {
		for _, c := range dedupCols {
			if s, ok := c.(string); ok {
				dedupCfg.Columns = append(dedupCfg.Columns, s)
			}
		}
	}
	if keep, ok := step.Params["dedup_keep"].(string); ok {
		dedupCfg.Keep = cleaning.DedupKeepStrategy(keep)
	}

	if formatRules, ok := step.Params["format_rules"].([]interface{}); ok {
		for _, r := range formatRules {
			if ruleMap, ok := r.(map[string]interface{}); ok {
				rule := cleaning.FormatRule{}
				if col, ok := ruleMap["column"].(string); ok {
					rule.Column = col
				}
				if strategy, ok := ruleMap["strategy"].(string); ok {
					rule.Strategy = cleaning.FormatStrategy(strategy)
				}
				if params, ok := ruleMap["params"].(map[string]string); ok {
					rule.Params = params
				}
				formatCfg.Rules = append(formatCfg.Rules, rule)
			}
		}
	}

	return missingCfg, outlierCfg, formatCfg, dedupCfg
}
