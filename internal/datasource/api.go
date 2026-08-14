package datasource

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type APIConfig struct {
	URL           string            `yaml:"url" json:"url"`
	Headers       map[string]string `yaml:"headers" json:"headers"`
	Pagination    *PaginationConfig `yaml:"pagination" json:"pagination"`
	Timeout       int               `yaml:"timeout" json:"timeout"`
	DataPath      string            `yaml:"data_path" json:"data_path"`
}

type PaginationConfig struct {
	Mode       string `yaml:"mode" json:"mode"`
	OffsetParam string `yaml:"offset_param" json:"offset_param"`
	LimitParam  string `yaml:"limit_param" json:"limit_param"`
	CursorParam string `yaml:"cursor_param" json:"cursor_param"`
	CursorPath  string `yaml:"cursor_path" json:"cursor_path"`
	PageSize    int    `yaml:"page_size" json:"page_size"`
	MaxPages    int    `yaml:"max_pages" json:"max_pages"`
}

func ReadAPI(cfg *APIConfig) (*Dataset, error) {
	client := &http.Client{}
	if cfg.Timeout > 0 {
		client.Timeout = time.Duration(cfg.Timeout) * time.Second
	} else {
		client.Timeout = 30 * time.Second
	}

	var allRecords []map[string]interface{}

	if cfg.Pagination == nil {
		records, err := fetchAPIPage(client, cfg, nil)
		if err != nil {
			return nil, err
		}
		allRecords = records
	} else {
		var err error
		allRecords, err = fetchAllPages(client, cfg)
		if err != nil {
			return nil, err
		}
	}

	if len(allRecords) == 0 {
		return &Dataset{Name: "api:" + cfg.URL}, nil
	}

	return DatasetFromMap("api:"+cfg.URL, allRecords)
}

func fetchAPIPage(client *http.Client, cfg *APIConfig, params map[string]string) ([]map[string]interface{}, error) {
	req, err := http.NewRequest("GET", cfg.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, resolveEnvVars(v))
	}

	q := req.URL.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	return extractRecords(raw, cfg.DataPath), nil
}

func extractRecords(raw interface{}, dataPath string) []map[string]interface{} {
	var target interface{} = raw

	if dataPath != "" {
		parts := strings.Split(dataPath, ".")
		for _, part := range parts {
			if m, ok := target.(map[string]interface{}); ok {
				target = m[part]
			} else {
				return nil
			}
		}
	}

	switch v := target.(type) {
	case []interface{}:
		var records []map[string]interface{}
		for _, item := range v {
			if obj, ok := item.(map[string]interface{}); ok {
				flat := make(map[string]interface{})
				flattenJSON(obj, "", flat)
				records = append(records, flat)
			}
		}
		return records
	case map[string]interface{}:
		flat := make(map[string]interface{})
		flattenJSON(v, "", flat)
		return []map[string]interface{}{flat}
	default:
		return nil
	}
}

func fetchAllPages(client *http.Client, cfg *APIConfig) ([]map[string]interface{}, error) {
	pg := cfg.Pagination
	var allRecords []map[string]interface{}
	maxPages := pg.MaxPages
	if maxPages <= 0 {
		maxPages = 1000
	}
	pageSize := pg.PageSize
	if pageSize <= 0 {
		pageSize = 100
	}

	switch pg.Mode {
	case "offset", "":
		offset := 0
		for page := 0; page < maxPages; page++ {
			params := map[string]string{
				pg.OffsetParam: strconv.Itoa(offset),
				pg.LimitParam:  strconv.Itoa(pageSize),
			}
			records, err := fetchAPIPage(client, cfg, params)
			if err != nil {
				return allRecords, err
			}
			if len(records) == 0 {
				break
			}
			allRecords = append(allRecords, records...)
			if len(records) < pageSize {
				break
			}
			offset += pageSize
		}

	case "cursor":
		cursor := ""
		for page := 0; page < maxPages; page++ {
			params := map[string]string{}
			if cursor != "" {
				params[pg.CursorParam] = cursor
			}
			if pg.LimitParam != "" {
				params[pg.LimitParam] = strconv.Itoa(pageSize)
			}

			resp, err := fetchAPIPageWithCursor(client, cfg, params, pg.CursorPath)
			if err != nil {
				return allRecords, err
			}
			if len(resp.Records) == 0 {
				break
			}
			allRecords = append(allRecords, resp.Records...)
			if resp.NextCursor == "" {
				break
			}
			cursor = resp.NextCursor
		}

	default:
		return nil, fmt.Errorf("unsupported pagination mode: %s", pg.Mode)
	}

	return allRecords, nil
}

type cursorResponse struct {
	Records    []map[string]interface{}
	NextCursor string
}

func fetchAPIPageWithCursor(client *http.Client, cfg *APIConfig, params map[string]string, cursorPath string) (*cursorResponse, error) {
	req, err := http.NewRequest("GET", cfg.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, resolveEnvVars(v))
	}

	q := req.URL.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	records := extractRecords(raw, cfg.DataPath)

	nextCursor := ""
	if cursorPath != "" {
		parts := strings.Split(cursorPath, ".")
		var target interface{} = raw
		for _, part := range parts {
			if m, ok := target.(map[string]interface{}); ok {
				target = m[part]
			}
		}
		if s, ok := target.(string); ok {
			nextCursor = s
		}
	}

	return &cursorResponse{Records: records, NextCursor: nextCursor}, nil
}
