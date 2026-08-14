package recommend

import (
	"regexp"
	"sort"
	"time"

	"github.com/data-cleaner/internal/datasource"
)

var patternDefinitions = []struct {
	Type    PatternType
	Pattern string
	Regex   *regexp.Regexp
}{
	{
		Type:    PatternEmail,
		Pattern: `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`,
	},
	{
		Type:    PatternIDCard,
		Pattern: `^[1-9]\d{5}(18|19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]$`,
	},
	{
		Type:    PatternPhone,
		Pattern: `^1[3-9]\d{9}$|^0\d{2,3}-?\d{7,8}$|^\+86-?1[3-9]\d{9}$`,
	},
	{
		Type:    PatternURL,
		Pattern: `^(https?://|www\.)[a-zA-Z0-9][-a-zA-Z0-9]{0,62}(\.[a-zA-Z0-9][-a-zA-Z0-9]{0,62})+(/[-a-zA-Z0-9@:%_+.~#?&/=]*)?$`,
	},
	{
		Type:    PatternIP,
		Pattern: `^((25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(25[0-5]|2[0-4]\d|[01]?\d\d?)$`,
	},
	{
		Type:    PatternDate,
		Pattern: `^\d{4}[-/](0[1-9]|1[0-2])[-/](0[1-9]|[12]\d|3[01])$|^\d{4}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])$`,
	},
	{
		Type:    PatternAmount,
		Pattern: `^-?\d+\.\d{1,2}$`,
	},
}

func init() {
	for i := range patternDefinitions {
		patternDefinitions[i].Regex = regexp.MustCompile(patternDefinitions[i].Pattern)
	}
}

func DetectPatterns(rows []datasource.Row, colIdx int) []PatternMatch {
	var matches []PatternMatch
	nonNullCount := 0
	counts := make(map[PatternType]int)

	for _, row := range rows {
		if colIdx >= len(row.Values) {
			continue
		}
		cell := row.Values[colIdx]
		if cell.IsNull {
			continue
		}

		nonNullCount++
		valStr := datasource.FormatCellValue(cell)

		for _, pd := range patternDefinitions {
			if pd.Regex.MatchString(valStr) {
				counts[pd.Type]++
			}
		}
	}

	if nonNullCount == 0 {
		return matches
	}

	for _, pd := range patternDefinitions {
		count := counts[pd.Type]
		rate := float64(count) / float64(nonNullCount)
		matches = append(matches, PatternMatch{
			Type:      pd.Type,
			MatchRate: rate,
			Pattern:   pd.Pattern,
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].MatchRate != matches[j].MatchRate {
			return matches[i].MatchRate > matches[j].MatchRate
		}
		return patternSpecificity[matches[i].Type] > patternSpecificity[matches[j].Type]
	})

	return matches
}

func GetBestPattern(matches []PatternMatch, threshold float64) *PatternMatch {
	var best *PatternMatch

	for _, m := range matches {
		if m.MatchRate >= threshold {
			if best == nil {
				best = &PatternMatch{
					Type:      m.Type,
					MatchRate: m.MatchRate,
					Pattern:   m.Pattern,
				}
			} else {
				if m.MatchRate > best.MatchRate {
					best = &PatternMatch{
						Type:      m.Type,
						MatchRate: m.MatchRate,
						Pattern:   m.Pattern,
					}
				} else if m.MatchRate == best.MatchRate {
					if patternSpecificity[m.Type] > patternSpecificity[best.Type] {
						best = &PatternMatch{
							Type:      m.Type,
							MatchRate: m.MatchRate,
							Pattern:   m.Pattern,
						}
					}
				}
			}
		}
	}

	return best
}

func DetectDateFormat(rows []datasource.Row, colIdx int) string {
	formats := []string{
		"2006-01-02",
		"2006/01/02",
		"20060102",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"01/02/2006",
		"02-01-2006",
	}

	formatCounts := make(map[string]int)
	total := 0

	for _, row := range rows {
		if colIdx >= len(row.Values) {
			continue
		}
		cell := row.Values[colIdx]
		if cell.IsNull {
			continue
		}

		valStr := datasource.FormatCellValue(cell)
		total++

		for _, fmt := range formats {
			if _, err := time.Parse(fmt, valStr); err == nil {
				formatCounts[fmt]++
				break
			}
		}
	}

	if total == 0 {
		return ""
	}

	bestFormat := ""
	bestCount := 0
	for fmt, count := range formatCounts {
		if count > bestCount {
			bestCount = count
			bestFormat = fmt
		}
	}

	if float64(bestCount)/float64(total) >= 0.9 {
		return bestFormat
	}

	return ""
}

func GetDateRange(rows []datasource.Row, colIdx int) (time.Time, time.Time, bool) {
	var min, max time.Time
	hasData := false

	for _, row := range rows {
		if colIdx >= len(row.Values) {
			continue
		}
		cell := row.Values[colIdx]
		if cell.IsNull {
			continue
		}

		var t time.Time
		switch cell.Type {
		case datasource.TypeDate:
			t = cell.DateVal
		default:
			valStr := datasource.FormatCellValue(cell)
			formats := []string{"2006-01-02", "2006/01/02", "20060102", "2006-01-02 15:04:05"}
			parsed := false
			for _, fmt := range formats {
				if pt, err := time.Parse(fmt, valStr); err == nil {
					t = pt
					parsed = true
					break
				}
			}
			if !parsed {
				continue
			}
		}

		if !hasData {
			min = t
			max = t
			hasData = true
		} else {
			if t.Before(min) {
				min = t
			}
			if t.After(max) {
				max = t
			}
		}
	}

	return min, max, hasData
}
