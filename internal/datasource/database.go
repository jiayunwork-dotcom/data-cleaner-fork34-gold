package datasource

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

type DBConfig struct {
	Driver   string `yaml:"driver" json:"driver"`
	DSN      string `yaml:"dsn" json:"dsn"`
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	User     string `yaml:"user" json:"user"`
	Password string `yaml:"password" json:"password"`
	Database string `yaml:"database" json:"database"`
	Table    string `yaml:"table" json:"table"`
	Query    string `yaml:"query" json:"query"`
}

func (c *DBConfig) buildDSN() string {
	if c.DSN != "" {
		return resolveEnvVars(c.DSN)
	}
	pw := resolveEnvVars(c.Password)
	switch strings.ToLower(c.Driver) {
	case "postgres", "postgresql":
		return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			c.Host, c.Port, c.User, pw, c.Database)
	case "mysql":
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", c.User, pw, c.Host, c.Port, c.Database)
	default:
		return c.DSN
	}
}

func ReadDatabase(cfg *DBConfig) (*Dataset, error) {
	dsn := cfg.buildDSN()
	driver := cfg.Driver
	if driver == "postgresql" {
		driver = "postgres"
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", driver, err)
	}
	defer db.Close()

	var query string
	if cfg.Query != "" {
		query = cfg.Query
	} else if cfg.Table != "" {
		query = fmt.Sprintf("SELECT * FROM %s", cfg.Table)
	} else {
		return nil, fmt.Errorf("must specify either table or query")
	}

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("get column types: %w", err)
	}

	ds := &Dataset{
		Name: fmt.Sprintf("%s:%s", driver, cfg.Table),
		Schema: Schema{
			Columns: make([]ColumnSchema, len(colTypes)),
		},
		Rows: make([]Row, 0),
	}

	for i, ct := range colTypes {
		ds.Schema.Columns[i] = ColumnSchema{
			Name:     ct.Name(),
			DataType: sqlTypeToDataType(ct.DatabaseTypeName()),
			Nullable: true,
		}
	}

	for rows.Next() {
		values := make([]interface{}, len(colTypes))
		valuePtrs := make([]interface{}, len(colTypes))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		row := Row{Values: make([]CellValue, len(colTypes))}
		for i := range colTypes {
			val := values[i]
			if val == nil {
				row.Values[i] = CellValue{IsNull: true, Type: ds.Schema.Columns[i].DataType}
				continue
			}
			switch v := val.(type) {
			case []byte:
				row.Values[i] = CellValue{StrVal: string(v), Raw: string(v), Type: ds.Schema.Columns[i].DataType}
			case string:
				row.Values[i] = CellValue{StrVal: v, Raw: v, Type: ds.Schema.Columns[i].DataType}
			case int64:
				row.Values[i] = CellValue{IntVal: v, Type: TypeInt, Raw: fmt.Sprintf("%d", v)}
			case float64:
				row.Values[i] = CellValue{FloatVal: v, Type: TypeFloat, Raw: fmt.Sprintf("%f", v)}
			case bool:
				row.Values[i] = CellValue{BoolVal: v, Type: TypeBool, Raw: fmt.Sprintf("%t", v)}
			default:
				s := fmt.Sprintf("%v", v)
				row.Values[i] = CellValue{StrVal: s, Raw: s, Type: TypeString}
			}
			row.Values[i].Type = ds.Schema.Columns[i].DataType
			convertCellValue(&row.Values[i])
		}
		ds.Rows = append(ds.Rows, row)
	}

	return ds, nil
}

func sqlTypeToDataType(sqlType string) DataType {
	sqlType = strings.ToUpper(sqlType)
	switch {
	case strings.Contains(sqlType, "INT"):
		return TypeInt
	case strings.Contains(sqlType, "FLOAT") || strings.Contains(sqlType, "DOUBLE") ||
		strings.Contains(sqlType, "DECIMAL") || strings.Contains(sqlType, "NUMERIC") ||
		strings.Contains(sqlType, "REAL"):
		return TypeFloat
	case strings.Contains(sqlType, "BOOL"):
		return TypeBool
	case strings.Contains(sqlType, "DATE") || strings.Contains(sqlType, "TIMESTAMP") ||
		strings.Contains(sqlType, "TIME"):
		return TypeDate
	default:
		return TypeString
	}
}

func CheckDBConnection(cfg *DBConfig) error {
	dsn := cfg.buildDSN()
	driver := cfg.Driver
	if driver == "postgresql" {
		driver = "postgres"
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}
