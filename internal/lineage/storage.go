package lineage

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	LineageDirName       = "lineage"
	DefaultHistoryCount  = 10
	CompressionThreshold = 10 * 1024 * 1024
)

type LineageStorage struct {
	baseDir      string
	lineageDir   string
	historyCount int
}

func NewLineageStorage(baseDir string, historyCount int) *LineageStorage {
	if historyCount <= 0 {
		historyCount = DefaultHistoryCount
	}

	lineageDir := filepath.Join(baseDir, LineageDirName)
	os.MkdirAll(lineageDir, 0755)

	return &LineageStorage{
		baseDir:      baseDir,
		lineageDir:   lineageDir,
		historyCount: historyCount,
	}
}

func (s *LineageStorage) Save(graph *LineageGraph) (string, error) {
	if err := s.ValidateGraph(graph); err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal lineage graph: %w", err)
	}

	timestamp := graph.Timestamp.Format("20060102_150405")
	filename := fmt.Sprintf("lineage_%s_%s", timestamp, graph.ID[:8])

	var filePath string

	if len(data) > CompressionThreshold {
		filePath = filepath.Join(s.lineageDir, filename+".json.gz")
		var compressed bytes.Buffer
		gw := gzip.NewWriter(&compressed)
		if _, err := gw.Write(data); err != nil {
			return "", fmt.Errorf("compress lineage data: %w", err)
		}
		if err := gw.Close(); err != nil {
			return "", fmt.Errorf("close gzip writer: %w", err)
		}
		if err := os.WriteFile(filePath, compressed.Bytes(), 0644); err != nil {
			return "", fmt.Errorf("write compressed lineage: %w", err)
		}
	} else {
		filePath = filepath.Join(s.lineageDir, filename+".json")
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return "", fmt.Errorf("write lineage file: %w", err)
		}
	}

	if err := s.pruneHistory(); err != nil {
		return "", fmt.Errorf("prune history: %w", err)
	}

	return filePath, nil
}

func (s *LineageStorage) Load(snapshotID string) (*LineageGraph, error) {
	filePath, err := s.findSnapshotFile(snapshotID)
	if err != nil {
		return nil, err
	}

	var data []byte

	if strings.HasSuffix(filePath, ".gz") {
		f, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("open compressed file: %w", err)
		}
		defer f.Close()

		gr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("create gzip reader: %w", err)
		}
		defer gr.Close()

		data, err = io.ReadAll(gr)
		if err != nil {
			return nil, fmt.Errorf("read compressed data: %w", err)
		}
	} else {
		data, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read lineage file: %w", err)
		}
	}

	return LineageGraphFromJSON(data)
}

func (s *LineageStorage) LoadLatest() (*LineageGraph, error) {
	snapshots, err := s.ListSnapshots()
	if err != nil {
		return nil, err
	}

	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no lineage snapshots found")
	}

	return s.Load(snapshots[0].ID)
}

func (s *LineageStorage) findSnapshotFile(snapshotID string) (string, error) {
	files, err := os.ReadDir(s.lineageDir)
	if err != nil {
		return "", fmt.Errorf("read lineage directory: %w", err)
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if strings.Contains(name, snapshotID) && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".json.gz")) {
			return filepath.Join(s.lineageDir, name), nil
		}
	}

	return "", fmt.Errorf("snapshot not found: %s", snapshotID)
}

func (s *LineageStorage) ListSnapshots() ([]SnapshotInfo, error) {
	files, err := os.ReadDir(s.lineageDir)
	if err != nil {
		return nil, fmt.Errorf("read lineage directory: %w", err)
	}

	var snapshots []SnapshotInfo

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if !strings.HasPrefix(name, "lineage_") {
			continue
		}
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".json.gz") {
			continue
		}

		info, err := f.Info()
		if err != nil {
			continue
		}

		snapshotInfo, err := s.parseSnapshotName(name, info.Size())
		if err != nil {
			continue
		}

		snapshots = append(snapshots, snapshotInfo)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.After(snapshots[j].Timestamp)
	})

	return snapshots, nil
}

func (s *LineageStorage) parseSnapshotName(name string, fileSize int64) (SnapshotInfo, error) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".json"), "_")
	if len(parts) < 3 {
		return SnapshotInfo{}, fmt.Errorf("invalid snapshot name: %s", name)
	}

	timestampStr := parts[1] + "_" + parts[2]
	id := ""
	if len(parts) >= 4 {
		id = parts[3]
	}

	timestamp, err := time.Parse("20060102_150405", timestampStr)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("parse timestamp: %w", err)
	}

	graph, err := s.Load(id)
	nodeCount := 0
	edgeCount := 0
	if err == nil && graph != nil {
		nodeCount = len(graph.Nodes)
		edgeCount = len(graph.Edges)
	}

	return SnapshotInfo{
		ID:           id,
		Timestamp:    timestamp,
		FileSize:     fileSize,
		IsCompressed: strings.HasSuffix(name, ".gz"),
		NodeCount:    nodeCount,
		EdgeCount:    edgeCount,
	}, nil
}

func (s *LineageStorage) pruneHistory() error {
	snapshots, err := s.ListSnapshots()
	if err != nil {
		return err
	}

	if len(snapshots) <= s.historyCount {
		return nil
	}

	for i := s.historyCount; i < len(snapshots); i++ {
		filePath, err := s.findSnapshotFile(snapshots[i].ID)
		if err != nil {
			continue
		}
		os.Remove(filePath)
	}

	return nil
}

func (s *LineageStorage) Delete(snapshotID string) error {
	filePath, err := s.findSnapshotFile(snapshotID)
	if err != nil {
		return err
	}
	return os.Remove(filePath)
}

func (s *LineageStorage) ValidateGraph(graph *LineageGraph) error {
	nodeMap := make(map[string]bool)
	for _, node := range graph.Nodes {
		nodeMap[node.ID] = true
	}

	for _, edge := range graph.Edges {
		if !nodeMap[edge.SourceNodeID] {
			return fmt.Errorf("edge references non-existent source node: %s", edge.SourceNodeID)
		}
		if !nodeMap[edge.TargetNodeID] {
			return fmt.Errorf("edge references non-existent target node: %s", edge.TargetNodeID)
		}
	}

	if hasCycle(graph) {
		return ErrCycleDetected
	}

	return nil
}

func hasCycle(graph *LineageGraph) bool {
	nodeMap := make(map[string]int)
	adj := make(map[string][]string)

	for _, edge := range graph.Edges {
		adj[edge.SourceNodeID] = append(adj[edge.SourceNodeID], edge.TargetNodeID)
	}

	var dfs func(string) bool
	dfs = func(nodeID string) bool {
		nodeMap[nodeID] = 1
		for _, next := range adj[nodeID] {
			if nodeMap[next] == 1 {
				return true
			}
			if nodeMap[next] == 0 {
				if dfs(next) {
					return true
				}
			}
		}
		nodeMap[nodeID] = 2
		return false
	}

	for _, node := range graph.Nodes {
		if nodeMap[node.ID] == 0 {
			if dfs(node.ID) {
				return true
			}
		}
	}

	return false
}
