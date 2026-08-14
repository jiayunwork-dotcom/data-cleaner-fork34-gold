package templates

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/data-cleaner/internal/quality"
	"github.com/data-cleaner/internal/rules"
	"gopkg.in/yaml.v3"
)

type Template struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Builtin     []rules.Rule   `yaml:"builtin"`
	CrossField  []rules.Rule   `yaml:"cross_field"`
	RangeChecks []quality.RangeCheckConfig `yaml:"range_checks"`
	ConsistencyRules []quality.ConsistencyRule `yaml:"consistency_rules"`
	ValidityRules    []quality.ValidityRule    `yaml:"validity_rules"`
}

var registry map[string]*Template

func init() {
	registry = make(map[string]*Template)
	registerFinanceTemplate()
	registerHealthcareTemplate()
	registerEcommerceTemplate()
}

func Register(t *Template) {
	registry[t.Name] = t
}

func Get(name string) (*Template, bool) {
	t, ok := registry[name]
	return t, ok
}

func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func All() map[string]*Template {
	return registry
}

func MergeTemplates(templateNames []string, userBuiltin []rules.Rule, userCrossField []rules.Rule, userRangeChecks []quality.RangeCheckConfig, userConsistencyRules []quality.ConsistencyRule, userValidityRules []quality.ValidityRule) ([]rules.Rule, []rules.Rule, []quality.RangeCheckConfig, []quality.ConsistencyRule, []quality.ValidityRule) {
	mergedBuiltin := make([]rules.Rule, 0)
	mergedCrossField := make([]rules.Rule, 0)
	mergedRangeChecks := make([]quality.RangeCheckConfig, 0)
	mergedConsistencyRules := make([]quality.ConsistencyRule, 0)
	mergedValidityRules := make([]quality.ValidityRule, 0)

	templateBuiltinKeys := make(map[string]bool)
	templateCrossFieldKeys := make(map[string]bool)
	templateRangeCheckKeys := make(map[string]bool)
	templateConsistencyKeys := make(map[string]bool)
	templateValidityKeys := make(map[string]bool)

	for _, name := range templateNames {
		t, ok := Get(name)
		if !ok {
			continue
		}
		for _, r := range t.Builtin {
			key := fmt.Sprintf("%s:%s", r.Field, r.Type)
			templateBuiltinKeys[key] = true
			mergedBuiltin = append(mergedBuiltin, r)
		}
		for _, r := range t.CrossField {
			key := r.ID
			templateCrossFieldKeys[key] = true
			mergedCrossField = append(mergedCrossField, r)
		}
		for _, rc := range t.RangeChecks {
			key := rc.Field
			templateRangeCheckKeys[key] = true
			mergedRangeChecks = append(mergedRangeChecks, rc)
		}
		for _, cr := range t.ConsistencyRules {
			key := fmt.Sprintf("%s_%s_%s", cr.Type, cr.FieldA, cr.FieldB)
			templateConsistencyKeys[key] = true
			mergedConsistencyRules = append(mergedConsistencyRules, cr)
		}
		for _, vr := range t.ValidityRules {
			key := fmt.Sprintf("%s:%s", vr.Field, vr.Pattern)
			templateValidityKeys[key] = true
			mergedValidityRules = append(mergedValidityRules, vr)
		}
	}

	for _, r := range userBuiltin {
		key := fmt.Sprintf("%s:%s", r.Field, r.Type)
		if templateBuiltinKeys[key] {
			mergedBuiltin = replaceBuiltinRule(mergedBuiltin, r)
		} else {
			mergedBuiltin = append(mergedBuiltin, r)
		}
	}
	for _, r := range userCrossField {
		if templateCrossFieldKeys[r.ID] {
			mergedCrossField = replaceCrossFieldRule(mergedCrossField, r)
		} else {
			mergedCrossField = append(mergedCrossField, r)
		}
	}
	for _, rc := range userRangeChecks {
		if templateRangeCheckKeys[rc.Field] {
			mergedRangeChecks = replaceRangeCheck(mergedRangeChecks, rc)
		} else {
			mergedRangeChecks = append(mergedRangeChecks, rc)
		}
	}
	for _, cr := range userConsistencyRules {
		key := fmt.Sprintf("%s_%s_%s", cr.Type, cr.FieldA, cr.FieldB)
		if templateConsistencyKeys[key] {
			mergedConsistencyRules = replaceConsistencyRule(mergedConsistencyRules, cr)
		} else {
			mergedConsistencyRules = append(mergedConsistencyRules, cr)
		}
	}
	for _, vr := range userValidityRules {
		key := fmt.Sprintf("%s:%s", vr.Field, vr.Pattern)
		if templateValidityKeys[key] {
			mergedValidityRules = replaceValidityRule(mergedValidityRules, vr)
		} else {
			mergedValidityRules = append(mergedValidityRules, vr)
		}
	}

	return mergedBuiltin, mergedCrossField, mergedRangeChecks, mergedConsistencyRules, mergedValidityRules
}

func replaceBuiltinRule(rules []rules.Rule, newRule rules.Rule) []rules.Rule {
	for i, r := range rules {
		if r.Field == newRule.Field && r.Type == newRule.Type {
			rules[i] = newRule
			return rules
		}
	}
	return rules
}

func replaceCrossFieldRule(rules []rules.Rule, newRule rules.Rule) []rules.Rule {
	for i, r := range rules {
		if r.ID == newRule.ID {
			rules[i] = newRule
			return rules
		}
	}
	return rules
}

func replaceRangeCheck(checks []quality.RangeCheckConfig, newCheck quality.RangeCheckConfig) []quality.RangeCheckConfig {
	for i, rc := range checks {
		if rc.Field == newCheck.Field {
			checks[i] = newCheck
			return checks
		}
	}
	return checks
}

func replaceConsistencyRule(rules []quality.ConsistencyRule, newRule quality.ConsistencyRule) []quality.ConsistencyRule {
	for i, r := range rules {
		if r.Type == newRule.Type && r.FieldA == newRule.FieldA && r.FieldB == newRule.FieldB {
			rules[i] = newRule
			return rules
		}
	}
	return rules
}

func replaceValidityRule(rules []quality.ValidityRule, newRule quality.ValidityRule) []quality.ValidityRule {
	for i, r := range rules {
		if r.Field == newRule.Field && r.Pattern == newRule.Pattern {
			rules[i] = newRule
			return rules
		}
	}
	return rules
}

func PrintTemplateList() {
	fmt.Println("\n╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║              Available Industry Rule Templates              ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	for _, name := range List() {
		t, _ := Get(name)
		fmt.Printf("  %-15s %s\n", t.Name, t.Description)
		fmt.Printf("  %-15s %d builtin rules, %d cross-field rules, %d quality checks\n",
			"", len(t.Builtin), len(t.CrossField), len(t.RangeChecks)+len(t.ConsistencyRules)+len(t.ValidityRules))
		fmt.Println()
	}

	fmt.Println("Use 'data-cleaner templates <name>' to see detailed rules for a template.")
	fmt.Println("Use 'data-cleaner templates --export <name>' to export a template as YAML.")
}

func PrintTemplateDetail(name string) {
	t, ok := Get(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "Template '%s' not found. Available: %s\n", name, strings.Join(List(), ", "))
		return
	}

	fmt.Printf("\n╔══════════════════════════════════════════════════════════════╗\n")
	fmt.Printf("║  Template: %-48s ║\n", t.Name)
	fmt.Printf("╚══════════════════════════════════════════════════════════════╝\n\n")
	fmt.Printf("  Description: %s\n", t.Description)
	fmt.Printf("  Version: %s\n\n", t.Version)

	if len(t.Builtin) > 0 {
		fmt.Println("  Builtin Rules:")
		fmt.Println("  ┌──────────────────────┬────────────────┬──────────┬──────────┐")
		fmt.Println("  │ ID                   │ Type           │ Field    │ Critical │")
		fmt.Println("  ├──────────────────────┼────────────────┼──────────┼──────────┤")
		for _, r := range t.Builtin {
			id := r.ID
			if len(id) > 20 {
				id = id[:20]
			}
			fieldName := r.Field
			if len(fieldName) > 8 {
				fieldName = fieldName[:8]
			}
			fmt.Printf("  │ %-20s │ %-14s │ %-8s │ %-8v │\n", id, r.Type, fieldName, r.Critical)
			if len(r.Params) > 0 {
				for k, v := range r.Params {
					fmt.Printf("  │ %-20s │ %-14s │ %8s │ %8v │\n", "", "", k, v)
				}
			}
		}
		fmt.Println("  └──────────────────────┴────────────────┴──────────┴──────────┘")
	}

	if len(t.CrossField) > 0 {
		fmt.Println("\n  Cross-Field Rules:")
		for _, r := range t.CrossField {
			fmt.Printf("    - %s (%s): ", r.ID, r.Type)
			parts := []string{}
			for k, v := range r.Params {
				parts = append(parts, fmt.Sprintf("%s=%v", k, v))
			}
			fmt.Println(strings.Join(parts, ", "))
		}
	}

	if len(t.RangeChecks) > 0 {
		fmt.Println("\n  Range Checks:")
		for _, rc := range t.RangeChecks {
			minStr := "-inf"
			maxStr := "+inf"
			if rc.Min != nil {
				minStr = fmt.Sprintf("%.0f", *rc.Min)
			}
			if rc.Max != nil {
				maxStr = fmt.Sprintf("%.0f", *rc.Max)
			}
			fmt.Printf("    - %s: [%s, %s]\n", rc.Field, minStr, maxStr)
		}
	}

	if len(t.ConsistencyRules) > 0 {
		fmt.Println("\n  Consistency Rules:")
		for _, cr := range t.ConsistencyRules {
			fmt.Printf("    - %s: %s %s %s\n", cr.Type, cr.FieldA, cr.Expression, cr.FieldB)
		}
	}

	if len(t.ValidityRules) > 0 {
		fmt.Println("\n  Validity Rules:")
		for _, vr := range t.ValidityRules {
			fmt.Printf("    - %s: pattern '%s'\n", vr.Field, vr.Pattern)
		}
	}
}

func ExportTemplateYAML(name string) (string, error) {
	t, ok := Get(name)
	if !ok {
		return "", fmt.Errorf("template '%s' not found", name)
	}

	type exportFormat struct {
		Template Template `yaml:"template"`
	}

	data, err := yaml.Marshal(&exportFormat{Template: *t})
	if err != nil {
		return "", fmt.Errorf("marshal template: %w", err)
	}

	return string(data), nil
}

func registerFinanceTemplate() {
	finance := &Template{
		Name:        "finance",
		Description: "Financial industry data quality rules (ID card, bank card, amounts, transaction time)",
		Version:     "1.0",
		Builtin: []rules.Rule{
			{
				ID:       "finance_id_card_length",
				Type:     "length",
				Field:    "id_card",
				Critical: true,
				Params:   map[string]interface{}{"min": float64(18), "max": float64(18)},
			},
			{
				ID:       "finance_id_card_checksum",
				Type:     "regex",
				Field:    "id_card",
				Critical: true,
				Params:   map[string]interface{}{"pattern": `^\d{17}[\dXx]$`},
			},
			{
				ID:       "finance_bank_card_luhn",
				Type:     "regex",
				Field:    "bank_card",
				Critical: true,
				Params:   map[string]interface{}{"pattern": `^\d{13,19}$`},
			},
			{
				ID:       "finance_amount_precision",
				Type:     "regex",
				Field:    "amount",
				Critical: true,
				Params:   map[string]interface{}{"pattern": `^\d+(\.\d{1,2})?$`},
			},
			{
				ID:       "finance_transaction_time_not_future",
				Type:     "cross_field",
				Field:    "transaction_time",
				Critical: true,
				Params:   map[string]interface{}{"field_a": "transaction_time", "field_b": "_current_time", "operator": "<="},
			},
		},
		RangeChecks: []quality.RangeCheckConfig{
			{Field: "amount", Min: float64Ptr(0)},
		},
		ValidityRules: []quality.ValidityRule{
			{Field: "id_card", Pattern: `^\d{17}[\dXx]$`},
			{Field: "bank_card", Pattern: `^\d{13,19}$`},
			{Field: "amount", Pattern: `^\d+(\.\d{1,2})?$`},
		},
	}
	Register(finance)
}

func registerHealthcareTemplate() {
	healthcare := &Template{
		Name:        "healthcare",
		Description: "Healthcare industry data quality rules (patient age, ICD-10, dates, temperature)",
		Version:     "1.0",
		Builtin: []rules.Rule{
			{
				ID:       "healthcare_age_range",
				Type:     "range",
				Field:    "age",
				Critical: true,
				Params:   map[string]interface{}{"min": float64(0), "max": float64(150)},
			},
			{
				ID:       "healthcare_icd10_format",
				Type:     "regex",
				Field:    "diagnosis_code",
				Critical: true,
				Params:   map[string]interface{}{"pattern": `^[A-Z]\d{2}(\.\d{1,4})?$`},
			},
			{
				ID:       "healthcare_temperature_range",
				Type:     "range",
				Field:    "temperature",
				Critical: true,
				Params:   map[string]interface{}{"min": float64(36), "max": float64(42)},
			},
		},
		CrossField: []rules.Rule{
			{
				ID:       "healthcare_admission_before_discharge",
				Type:     "cross_field",
				Field:    "admission_date",
				Critical: true,
				Params:   map[string]interface{}{"field_a": "admission_date", "field_b": "discharge_date", "operator": "<="},
			},
		},
		RangeChecks: []quality.RangeCheckConfig{
			{Field: "age", Min: float64Ptr(0), Max: float64Ptr(150)},
			{Field: "temperature", Min: float64Ptr(36), Max: float64Ptr(42)},
		},
		ConsistencyRules: []quality.ConsistencyRule{
			{Type: "compare", FieldA: "admission_date", FieldB: "discharge_date", Expression: "<="},
		},
		ValidityRules: []quality.ValidityRule{
			{Field: "diagnosis_code", Pattern: `^[A-Z]\d{2}(\.\d{1,4})?$`},
		},
	}
	Register(healthcare)
}

func registerEcommerceTemplate() {
	ecommerce := &Template{
		Name:        "ecommerce",
		Description: "E-commerce industry data quality rules (price, stock, SKU, order amount)",
		Version:     "1.0",
		Builtin: []rules.Rule{
			{
				ID:       "ecommerce_price_positive",
				Type:     "range",
				Field:    "price",
				Critical: true,
				Params:   map[string]interface{}{"min": float64(0.01)},
			},
			{
				ID:       "ecommerce_stock_nonneg_int",
				Type:     "regex",
				Field:    "stock",
				Critical: true,
				Params:   map[string]interface{}{"pattern": `^\d+$`},
			},
			{
				ID:       "ecommerce_sku_format",
				Type:     "regex",
				Field:    "sku",
				Critical: false,
				Params:   map[string]interface{}{"pattern": `^[A-Z0-9][A-Z0-9\-_]{2,49}$`},
			},
			{
				ID:       "ecommerce_order_amount_check",
				Type:     "cross_field",
				Field:    "order_amount",
				Critical: true,
				Params:   map[string]interface{}{"field_a": "order_amount", "field_b": "unit_price", "operator": "amount_equals_price_times_qty"},
			},
		},
		RangeChecks: []quality.RangeCheckConfig{
			{Field: "price", Min: float64Ptr(0.01)},
			{Field: "stock", Min: float64Ptr(0)},
		},
		ValidityRules: []quality.ValidityRule{
			{Field: "stock", Pattern: `^\d+$`},
			{Field: "sku", Pattern: `^[A-Z0-9][A-Z0-9\-_]{2,49}$`},
		},
	}
	Register(ecommerce)
}

func float64Ptr(v float64) *float64 {
	return &v
}
