package recommend

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/data-cleaner/internal/config"
	"github.com/data-cleaner/internal/datasource"
	"github.com/data-cleaner/internal/rules"
	"gopkg.in/yaml.v3"
)

func PrintRecommendations(recommendations []RuleRecommendation, result *AnalysisResult) {
	if result.IsSampled {
		fmt.Printf("\n📊 分析说明: 数据集超过 %d 行，已使用 %d 行采样进行统计分析（空值率和唯一值为全量扫描）\n\n",
			FullScanThreshold, result.SampleSize)
	}

	if len(recommendations) == 0 {
		fmt.Println("✅ 没有找到符合条件的推荐规则。")
		return
	}

	fmt.Printf("📋 共发现 %d 条推荐规则（按置信度排序）:\n\n", len(recommendations))

	headers := []string{"置信度", "规则类型", "目标字段", "参数", "推荐理由"}
	rows := make([][]string, 0, len(recommendations))

	for _, rec := range recommendations {
		paramsStr := formatParams(rec.Params, rec.Type, rec.Field, result)
		rows = append(rows, []string{
			fmt.Sprintf("%d%%", rec.Confidence),
			rec.Type,
			rec.Field,
			paramsStr,
			rec.Reason,
		})
	}

	printTable(headers, rows)
}

func isDateColumn(field string, result *AnalysisResult) bool {
	if result == nil || result.Schema == nil {
		return false
	}
	for _, col := range result.Schema.Columns {
		if col.Name == field {
			return col.DataType == datasource.TypeDate
		}
	}
	return false
}

func formatParams(params map[string]interface{}, ruleType string, field string, result *AnalysisResult) string {
	if len(params) == 0 {
		return "-"
	}

	isDateRange := ruleType == "range" && isDateColumn(field, result)

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		v := params[k]
		var vStr string
		switch val := v.(type) {
		case []interface{}:
			if len(val) > 5 {
				vStr = fmt.Sprintf("%v...(共%d项)", val[:5], len(val))
			} else {
				vStr = fmt.Sprintf("%v", val)
			}
		case float64:
			if isDateRange && (k == "min" || k == "max") {
				t := time.Unix(int64(val), 0)
				vStr = t.Format("2006-01-02")
			} else {
				if val == float64(int64(val)) {
					vStr = fmt.Sprintf("%d", int64(val))
				} else {
					vStr = fmt.Sprintf("%.2f", val)
				}
			}
		default:
			vStr = fmt.Sprintf("%v", v)
		}
		if len(vStr) > 30 {
			vStr = vStr[:27] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, vStr))
	}

	return strings.Join(parts, ", ")
}

func printTable(headers []string, rows [][]string) {
	colWidths := make([]int, len(headers))
	for i, h := range headers {
		colWidths[i] = len(h)
	}

	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}

	maxWidths := []int{8, 15, 20, 40, 50}
	for i := range colWidths {
		if colWidths[i] > maxWidths[i] {
			colWidths[i] = maxWidths[i]
		}
	}

	separator := "+"
	for _, w := range colWidths {
		separator += strings.Repeat("-", w+2) + "+"
	}

	fmt.Println(separator)

	headerRow := "|"
	for i, h := range headers {
		headerRow += fmt.Sprintf(" %-*s |", colWidths[i], h)
	}
	fmt.Println(headerRow)
	fmt.Println(separator)

	for _, row := range rows {
		dataRow := "|"
		for i, cell := range row {
			if len(cell) > colWidths[i] {
				cell = cell[:colWidths[i]-3] + "..."
			}
			dataRow += fmt.Sprintf(" %-*s |", colWidths[i], cell)
		}
		fmt.Println(dataRow)
	}
	fmt.Println(separator)
}

func ToYAML(recommendations []RuleRecommendation) (string, error) {
	type yamlRule struct {
		ID       string                 `yaml:"id"`
		Type     string                 `yaml:"type"`
		Field    string                 `yaml:"field"`
		Critical bool                   `yaml:"critical"`
		Params   map[string]interface{} `yaml:"params,omitempty"`
		Message  string                 `yaml:"message,omitempty"`
	}

	var rules []yamlRule
	for _, rec := range recommendations {
		r := yamlRule{
			ID:       rec.ID,
			Type:     rec.Type,
			Field:    rec.Field,
			Critical: rec.Confidence >= 90,
			Params:   rec.Params,
			Message:  rec.Reason,
		}
		rules = append(rules, r)
	}

	type wrapper struct {
		Rules struct {
			Builtin []yamlRule `yaml:"builtin"`
		} `yaml:"rules"`
	}

	w := wrapper{}
	w.Rules.Builtin = rules

	data, err := yaml.Marshal(w)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func ApplyToConfig(cfgPath string, recommendations []RuleRecommendation, yesToAll bool) error {
	if len(recommendations) == 0 {
		fmt.Println("没有可应用的推荐规则。")
		return nil
	}

	if !yesToAll {
		fmt.Printf("\n⚠️  将向配置文件 %s 追加 %d 条新规则。\n", cfgPath, len(recommendations))
		fmt.Println("变更摘要:")
		for _, rec := range recommendations {
			fmt.Printf("  - [%d%%] %s: %s -> %s\n", rec.Confidence, rec.Type, rec.Field, rec.Reason)
		}

		fmt.Print("\n确认继续? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("读取输入失败: %w", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("已取消。")
			return nil
		}
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	var newRules []rules.Rule
	for _, rec := range recommendations {
		rule := rules.Rule{
			ID:       rec.ID,
			Type:     rec.Type,
			Field:    rec.Field,
			Critical: rec.Confidence >= 90,
			Params:   rec.Params,
			Message:  rec.Reason,
		}
		newRules = append(newRules, rule)
	}

	cfg.Rules.Builtin = append(cfg.Rules.Builtin, newRules...)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	err = os.WriteFile(cfgPath, data, 0644)
	if err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}

	fmt.Printf("\n✅ 已成功向配置文件追加 %d 条新规则。\n", len(newRules))
	return nil
}

func PrintChangeSummary(recommendations []RuleRecommendation) {
	fmt.Println("\n📝 变更摘要:")
	fmt.Println(strings.Repeat("=", 60))

	typeCount := make(map[string]int)
	fieldCount := make(map[string]int)
	confidenceDistribution := map[string]int{
		"90-100": 0,
		"80-89":  0,
		"70-79":  0,
	}

	for _, rec := range recommendations {
		typeCount[rec.Type]++
		fieldCount[rec.Field]++

		switch {
		case rec.Confidence >= 90:
			confidenceDistribution["90-100"]++
		case rec.Confidence >= 80:
			confidenceDistribution["80-89"]++
		default:
			confidenceDistribution["70-79"]++
		}
	}

	fmt.Println("\n规则类型分布:")
	for t, c := range typeCount {
		fmt.Printf("  %-15s: %d 条\n", t, c)
	}

	fmt.Println("\n置信度分布:")
	for r, c := range confidenceDistribution {
		fmt.Printf("  %-15s: %d 条\n", r, c)
	}

	fmt.Println("\n涉及字段:")
	for f, c := range fieldCount {
		fmt.Printf("  %-20s: %d 条规则\n", f, c)
	}

	fmt.Println(strings.Repeat("=", 60))
}
