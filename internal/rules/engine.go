package rules

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/data-cleaner/internal/datasource"
)

type RuleType int

const (
	RuleNotNull RuleType = iota
	RuleTypeCheck
	RuleRange
	RuleEnum
	RuleRegex
	RuleLength
	RuleUnique
	RuleCrossField
	RuleCrossTable
	RuleDSL
)

type Rule struct {
	ID       string                 `yaml:"id" json:"id"`
	Type     string                 `yaml:"type" json:"type"`
	Field    string                 `yaml:"field" json:"field"`
	Critical bool                   `yaml:"critical" json:"critical"`
	Params   map[string]interface{} `yaml:"params" json:"params"`
	Message  string                 `yaml:"message" json:"message"`
}

type RuleResult struct {
	RuleID    string `json:"rule_id"`
	RowIndex  int    `json:"row_index"`
	Field     string `json:"field"`
	Passed    bool   `json:"passed"`
	Message   string `json:"message"`
	Value     string `json:"value"`
	IsCritical bool  `json:"is_critical"`
}

type Engine struct {
	Rules       []Rule
	CrossTable  []CrossTableRule
	DSLRules    []DSLRule
}

type CrossTableRule struct {
	ID         string `yaml:"id" json:"id"`
	Type       string `yaml:"type" json:"type"`
	ForeignKey string `yaml:"foreign_key" json:"foreign_key"`
	RefTable   string `yaml:"ref_table" json:"ref_table"`
	RefKey     string `yaml:"ref_key" json:"ref_key"`
	FieldA     string `yaml:"field_a" json:"field_a"`
	FieldB     string `yaml:"field_b" json:"field_b"`
	TableA     string `yaml:"table_a" json:"table_a"`
	TableB     string `yaml:"table_b" json:"table_b"`
	Critical   bool   `yaml:"critical" json:"critical"`
}

type DSLRule struct {
	ID         string `yaml:"id" json:"id"`
	Expression string `yaml:"expression" json:"expression"`
	Critical   bool   `yaml:"critical" json:"critical"`
}

func NewEngine(rules []Rule, crossTable []CrossTableRule, dslRules []DSLRule) *Engine {
	return &Engine{
		Rules:      rules,
		CrossTable: crossTable,
		DSLRules:   dslRules,
	}
}

func (e *Engine) Evaluate(ds *datasource.Dataset) []RuleResult {
	var results []RuleResult

	for _, rule := range e.Rules {
		colIdx := ds.Schema.ColumnIndex(rule.Field)
		if colIdx < 0 && rule.Type != "cross_field" {
			continue
		}

		for rowIdx, row := range ds.Rows {
			var cell datasource.CellValue
			if colIdx >= 0 && colIdx < len(row.Values) {
				cell = row.Values[colIdx]
			}

			passed, msg := e.evaluateRule(rule, cell, row, ds)
			results = append(results, RuleResult{
				RuleID:     rule.ID,
				RowIndex:   rowIdx,
				Field:      rule.Field,
				Passed:     passed,
				Message:    msg,
				Value:      datasource.FormatCellValue(cell),
				IsCritical: rule.Critical,
			})
		}
	}

	return results
}

func (e *Engine) evaluateRule(rule Rule, cell datasource.CellValue, row datasource.Row, ds *datasource.Dataset) (bool, string) {
	switch rule.Type {
	case "not_null":
		return evalNotNull(cell, rule)
	case "type_check":
		return evalTypeCheck(cell, rule, ds)
	case "range":
		return evalRange(cell, rule)
	case "enum":
		return evalEnum(cell, rule)
	case "regex":
		return evalRegex(cell, rule)
	case "length":
		return evalLength(cell, rule)
	case "unique":
		return true, "unique checked separately"
	case "cross_field":
		return evalCrossField(cell, row, rule, ds)
	default:
		return true, fmt.Sprintf("unknown rule type: %s", rule.Type)
	}
}

func evalNotNull(cell datasource.CellValue, rule Rule) (bool, string) {
	if cell.IsNull || cell.Raw == "" {
		return false, fmt.Sprintf("field %s is null", rule.Field)
	}
	return true, ""
}

func evalTypeCheck(cell datasource.CellValue, rule Rule, ds *datasource.Dataset) (bool, string) {
	if cell.IsNull {
		return true, ""
	}
	colIdx := ds.Schema.ColumnIndex(rule.Field)
	if colIdx < 0 {
		return false, fmt.Sprintf("field %s not found", rule.Field)
	}
	expected := ds.Schema.Columns[colIdx].DataType
	detected := datasource.TypeString
	if cell.Raw != "" {
		detected = detectTypeFromRaw(cell.Raw)
	}
	if detected == expected || detected == datasource.TypeUnknown {
		return true, ""
	}
	if detected == datasource.TypeInt && expected == datasource.TypeFloat {
		return true, ""
	}
	return false, fmt.Sprintf("type mismatch for %s: expected %s, got %s", rule.Field, expected, detected)
}

func detectTypeFromRaw(s string) datasource.DataType {
	return datasource.TypeString
}

func evalRange(cell datasource.CellValue, rule Rule) (bool, string) {
	if cell.IsNull {
		return true, ""
	}
	val := cellToFloat64(cell)
	min, hasMin := getFloatParam(rule.Params, "min")
	max, hasMax := getFloatParam(rule.Params, "max")

	if hasMin && val < min {
		return false, fmt.Sprintf("%s value %v below minimum %v", rule.Field, val, min)
	}
	if hasMax && val > max {
		return false, fmt.Sprintf("%s value %v above maximum %v", rule.Field, val, max)
	}
	return true, ""
}

func evalEnum(cell datasource.CellValue, rule Rule) (bool, string) {
	if cell.IsNull {
		return true, ""
	}
	values, ok := rule.Params["values"]
	if !ok {
		return true, ""
	}
	valList, ok := values.([]interface{})
	if !ok {
		return true, ""
	}
	cellStr := datasource.FormatCellValue(cell)
	for _, v := range valList {
		if fmt.Sprintf("%v", v) == cellStr {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s value '%s' not in enum list", rule.Field, cellStr)
}

func evalRegex(cell datasource.CellValue, rule Rule) (bool, string) {
	if cell.IsNull {
		return true, ""
	}
	pattern, ok := rule.Params["pattern"].(string)
	if !ok {
		return true, ""
	}
	pattern = strings.ReplaceAll(pattern, "\\\\", "\\")
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Sprintf("invalid regex pattern: %s", pattern)
	}
	cellStr := datasource.FormatCellValue(cell)
	if re.MatchString(cellStr) {
		return true, ""
	}
	return false, fmt.Sprintf("%s value '%s' doesn't match pattern '%s'", rule.Field, cellStr, pattern)
}

func evalLength(cell datasource.CellValue, rule Rule) (bool, string) {
	if cell.IsNull {
		return true, ""
	}
	cellStr := datasource.FormatCellValue(cell)
	length := len(cellStr)
	minLen, hasMin := getFloatParam(rule.Params, "min")
	maxLen, hasMax := getFloatParam(rule.Params, "max")

	if hasMin && float64(length) < minLen {
		return false, fmt.Sprintf("%s length %d below minimum %v", rule.Field, length, minLen)
	}
	if hasMax && float64(length) > maxLen {
		return false, fmt.Sprintf("%s length %d above maximum %v", rule.Field, length, maxLen)
	}
	return true, ""
}

func evalCrossField(cell datasource.CellValue, row datasource.Row, rule Rule, ds *datasource.Dataset) (bool, string) {
	fieldA, _ := rule.Params["field_a"].(string)
	fieldB, _ := rule.Params["field_b"].(string)
	op, _ := rule.Params["operator"].(string)
	conditionField, _ := rule.Params["condition_field"].(string)
	conditionValue, _ := rule.Params["condition_value"].(string)

	if conditionField != "" {
		condIdx := ds.Schema.ColumnIndex(conditionField)
		if condIdx >= 0 && condIdx < len(row.Values) {
			condCell := row.Values[condIdx]
			if datasource.FormatCellValue(condCell) != conditionValue {
				return true, ""
			}
		}
	}

	if op == "required_when" {
		idxB := ds.Schema.ColumnIndex(fieldB)
		if idxB >= 0 && idxB < len(row.Values) {
			if row.Values[idxB].IsNull {
				return false, fmt.Sprintf("%s is required when %s=%s", fieldB, conditionField, conditionValue)
			}
		}
		return true, ""
	}

	if op == "mutually_exclusive" {
		idxA := ds.Schema.ColumnIndex(fieldA)
		idxB := ds.Schema.ColumnIndex(fieldB)
		if idxA >= 0 && idxA < len(row.Values) && idxB >= 0 && idxB < len(row.Values) {
			aNull := row.Values[idxA].IsNull
			bNull := row.Values[idxB].IsNull
			if !aNull && !bNull {
				return false, fmt.Sprintf("%s and %s are mutually exclusive", fieldA, fieldB)
			}
		}
		return true, ""
	}

	idxA := ds.Schema.ColumnIndex(fieldA)
	idxB := ds.Schema.ColumnIndex(fieldB)
	if idxA < 0 || idxB < 0 {
		return true, ""
	}
	if idxA >= len(row.Values) || idxB >= len(row.Values) {
		return true, ""
	}

	cellA := row.Values[idxA]
	cellB := row.Values[idxB]
	if cellA.IsNull || cellB.IsNull {
		return true, ""
	}

	valA := cellToFloat64(cellA)
	valB := cellToFloat64(cellB)

	switch op {
	case ">":
		return valA > valB, fmt.Sprintf("%s(%v) > %s(%v)", fieldA, valA, fieldB, valB)
	case ">=":
		return valA >= valB, fmt.Sprintf("%s(%v) >= %s(%v)", fieldA, valA, fieldB, valB)
	case "<":
		return valA < valB, fmt.Sprintf("%s(%v) < %s(%v)", fieldA, valA, fieldB, valB)
	case "<=":
		return valA <= valB, fmt.Sprintf("%s(%v) <= %s(%v)", fieldA, valA, fieldB, valB)
	case "==":
		return valA == valB, fmt.Sprintf("%s(%v) == %s(%v)", fieldA, valA, fieldB, valB)
	case "sum_equals":
		if sumFields, ok := rule.Params["sum_fields"].([]interface{}); ok {
			total := 0.0
			for _, f := range sumFields {
				if fname, ok := f.(string); ok {
					idx := ds.Schema.ColumnIndex(fname)
					if idx >= 0 && idx < len(row.Values) && !row.Values[idx].IsNull {
						total += cellToFloat64(row.Values[idx])
					}
				}
			}
			return math.Abs(valA-total) < 0.001, fmt.Sprintf("sum(%v) != %v", total, valA)
		}
	}

	return true, ""
}

func (e *Engine) EvaluateCrossTable(datasets map[string]*datasource.Dataset) []RuleResult {
	var results []RuleResult

	for _, rule := range e.CrossTable {
		switch rule.Type {
		case "referential_integrity":
			results = append(results, e.checkReferentialIntegrity(rule, datasets)...)
		case "consistency":
			results = append(results, e.checkCrossTableConsistency(rule, datasets)...)
		}
	}

	return results
}

func (e *Engine) checkReferentialIntegrity(rule CrossTableRule, datasets map[string]*datasource.Dataset) []RuleResult {
	var results []RuleResult

	sourceDS, ok := datasets[rule.TableA]
	if !ok {
		return results
	}
	refDS, ok := datasets[rule.TableB]
	if !ok {
		return results
	}

	fkIdx := sourceDS.Schema.ColumnIndex(rule.ForeignKey)
	refIdx := refDS.Schema.ColumnIndex(rule.RefKey)
	if fkIdx < 0 || refIdx < 0 {
		return results
	}

	refValues := make(map[string]bool)
	for _, row := range refDS.Rows {
		if refIdx < len(row.Values) {
			refValues[datasource.FormatCellValue(row.Values[refIdx])] = true
		}
	}

	for rowIdx, row := range sourceDS.Rows {
		if fkIdx >= len(row.Values) {
			continue
		}
		fkVal := datasource.FormatCellValue(row.Values[fkIdx])
		if row.Values[fkIdx].IsNull {
			continue
		}
		if !refValues[fkVal] {
			results = append(results, RuleResult{
				RuleID:     rule.ID,
				RowIndex:   rowIdx,
				Field:      rule.ForeignKey,
				Passed:     false,
				Message:    fmt.Sprintf("foreign key '%s' not found in %s.%s", fkVal, rule.RefTable, rule.RefKey),
				Value:      fkVal,
				IsCritical: rule.Critical,
			})
		}
	}

	return results
}

func (e *Engine) checkCrossTableConsistency(rule CrossTableRule, datasets map[string]*datasource.Dataset) []RuleResult {
	var results []RuleResult

	dsA, ok := datasets[rule.TableA]
	if !ok {
		return results
	}
	dsB, ok := datasets[rule.TableB]
	if !ok {
		return results
	}

	keyAIdx := dsA.Schema.ColumnIndex(rule.ForeignKey)
	keyBIdx := dsB.Schema.ColumnIndex(rule.RefKey)
	fieldAIdx := dsA.Schema.ColumnIndex(rule.FieldA)
	fieldBIdx := dsB.Schema.ColumnIndex(rule.FieldB)

	if keyAIdx < 0 || keyBIdx < 0 || fieldAIdx < 0 || fieldBIdx < 0 {
		return results
	}

	bMap := make(map[string]string)
	for _, row := range dsB.Rows {
		if keyBIdx < len(row.Values) && fieldBIdx < len(row.Values) {
			key := datasource.FormatCellValue(row.Values[keyBIdx])
			val := datasource.FormatCellValue(row.Values[fieldBIdx])
			bMap[key] = val
		}
	}

	for rowIdx, row := range dsA.Rows {
		if keyAIdx >= len(row.Values) || fieldAIdx >= len(row.Values) {
			continue
		}
		key := datasource.FormatCellValue(row.Values[keyAIdx])
		valA := datasource.FormatCellValue(row.Values[fieldAIdx])
		if valB, ok := bMap[key]; ok {
			if valA != valB {
				results = append(results, RuleResult{
					RuleID:     rule.ID,
					RowIndex:   rowIdx,
					Field:      rule.FieldA,
					Passed:     false,
					Message:    fmt.Sprintf("inconsistency: %s=%s but %s.%s=%s", rule.FieldA, valA, rule.TableB, rule.FieldB, valB),
					Value:      valA,
					IsCritical: rule.Critical,
				})
			}
		}
	}

	return results
}

func cellToFloat64(cell datasource.CellValue) float64 {
	switch cell.Type {
	case datasource.TypeInt:
		return float64(cell.IntVal)
	case datasource.TypeFloat:
		return cell.FloatVal
	case datasource.TypeBool:
		if cell.BoolVal {
			return 1
		}
		return 0
	case datasource.TypeDate:
		return float64(cell.DateVal.Unix())
	default:
		return math.NaN()
	}
}

func getFloatParam(params map[string]interface{}, key string) (float64, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case string:
		f, err := parseFloat(val)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func parseFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

func ValidateRules(rules []Rule) []error {
	var errs []error
	for _, rule := range rules {
		switch rule.Type {
		case "regex":
			if pattern, ok := rule.Params["pattern"].(string); ok {
				if _, err := regexp.Compile(pattern); err != nil {
					errs = append(errs, fmt.Errorf("rule %s: invalid regex '%s': %w", rule.ID, pattern, err))
				}
			}
		case "range":
			if _, hasMin := rule.Params["min"]; !hasMin {
				if _, hasMax := rule.Params["max"]; !hasMax {
					errs = append(errs, fmt.Errorf("rule %s: range rule needs min or max", rule.ID))
				}
			}
		}
	}
	return errs
}
