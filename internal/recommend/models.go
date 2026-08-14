package recommend

import (
	"github.com/data-cleaner/internal/datasource"
)

type PatternType string

const (
	PatternEmail     PatternType = "email"
	PatternPhone     PatternType = "phone"
	PatternDate      PatternType = "date"
	PatternURL       PatternType = "url"
	PatternIP        PatternType = "ip"
	PatternIDCard    PatternType = "id_card"
	PatternAmount    PatternType = "amount"
	PatternGeneric   PatternType = "generic"
)

var patternSpecificity = map[PatternType]int{
	PatternEmail:   100,
	PatternIDCard:  95,
	PatternPhone:   90,
	PatternURL:     85,
	PatternIP:      80,
	PatternDate:    70,
	PatternAmount:  60,
	PatternGeneric: 0,
}

type PatternMatch struct {
	Type      PatternType
	MatchRate float64
	Pattern   string
}

type ColumnStats struct {
	ColumnName       string
	DataType         datasource.DataType
	TotalRows        int
	NullCount        int
	NullRate         float64
	UniqueCount      int
	UniqueRate       float64
	NumericStats     *NumericStats
	StringStats      *StringStats
	TopValues        []ValueCount
	TypeDistribution map[datasource.DataType]int
	PatternMatches   []PatternMatch
	BestPattern      *PatternMatch
	IsSampled        bool
}

type NumericStats struct {
	Min    float64
	Max    float64
	Mean   float64
	StdDev float64
}

type StringStats struct {
	MinLength    int
	MaxLength    int
	AvgLength    float64
	LengthCounts map[int]int
}

type ValueCount struct {
	Value string
	Count int
}

type ColumnRelation struct {
	Type         RelationType
	ColumnA      string
	ColumnB      string
	Confidence   float64
	MatchCount   int
	TotalCount   int
}

type RelationType string

const (
	RelationForeignKey    RelationType = "foreign_key"
	RelationFunctionalDep RelationType = "functional_dependency"
	RelationTimeOrder     RelationType = "time_order"
)

type RuleRecommendation struct {
	ID          string
	Type        string
	Field       string
	Params      map[string]interface{}
	Confidence  int
	Reason      string
	MatchRate   float64
	SampleSize  int
	PatternConsistency float64
}

type AnalysisResult struct {
	ColumnStats   map[string]*ColumnStats
	Relations     []ColumnRelation
	TotalRows     int
	IsSampled     bool
	SampleSize    int
	Schema        *datasource.Schema
	SampledRows   []datasource.Row
	AllRows       []datasource.Row
}

type RecommendConfig struct {
	MinConfidence int
	FocusColumns  []string
	ApplyToConfig bool
	YesToAll      bool
	OutputYAML    bool
}
