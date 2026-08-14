package datasource

import (
	"strings"
	"time"
)

type DataType int

const (
	TypeUnknown DataType = iota
	TypeInt
	TypeFloat
	TypeString
	TypeDate
	TypeBool
)

func (dt DataType) String() string {
	switch dt {
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeDate:
		return "date"
	case TypeBool:
		return "bool"
	default:
		return "unknown"
	}
}

func DataTypeFromString(s string) DataType {
	switch s {
	case "int", "integer":
		return TypeInt
	case "float", "double", "decimal":
		return TypeFloat
	case "string", "text", "varchar":
		return TypeString
	case "date", "datetime", "timestamp":
		return TypeDate
	case "bool", "boolean":
		return TypeBool
	default:
		return TypeUnknown
	}
}

type ColumnSchema struct {
	Name     string   `json:"name" yaml:"name"`
	DataType DataType `json:"data_type" yaml:"data_type"`
	Nullable bool     `json:"nullable" yaml:"nullable"`
}

type Schema struct {
	Columns []ColumnSchema `json:"columns" yaml:"columns"`
}

func (s *Schema) ColumnIndex(name string) int {
	for i, c := range s.Columns {
		if c.Name == strings.ToLower(name) {
			return i
		}
	}
	return -1
}

func (s *Schema) ColumnNames() []string {
	names := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		names[i] = c.Name
	}
	return names
}

type CellValue struct {
	Raw      string
	IntVal   int64
	FloatVal float64
	StrVal   string
	DateVal  time.Time
	BoolVal  bool
	IsNull   bool
	Type     DataType
}

type Row struct {
	Values []CellValue
}

type Dataset struct {
	Name   string
	Schema Schema
	Rows   []Row
}
