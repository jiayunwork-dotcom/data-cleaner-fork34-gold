package lineage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/data-cleaner/internal/datasource"
	"github.com/hashicorp/go-uuid"
)

type NodeType string

const (
	NodeTypeSource    NodeType = "source"
	NodeTypeTransform NodeType = "transform"
	NodeTypeOutput    NodeType = "output"
)

type TransformType string

const (
	TransformMerge   TransformType = "merge"
	TransformClean   TransformType = "clean"
	TransformFilter  TransformType = "filter"
	TransformEnrich  TransformType = "enrich"
	TransformAssess  TransformType = "assess"
	TransformOutput  TransformType = "output"
)

type ColumnContributionType string

const (
	ContributionUnion  ColumnContributionType = "union"
	ContributionJoin   ColumnContributionType = "join"
	ContributionDerive ColumnContributionType = "derive"
	ContributionCopy   ColumnContributionType = "copy"
)

type LineageNode struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        NodeType  `json:"type"`
	CreatedAt   time.Time `json:"created_at"`
	RowCount    int       `json:"row_count"`
	ColumnCount int       `json:"column_count"`
	Fingerprint string    `json:"fingerprint"`
	Columns     []string  `json:"columns"`
}

type ColumnLineage struct {
	TargetColumn     string                 `json:"target_column"`
	SourceColumns    []string               `json:"source_columns"`
	ContributionType ColumnContributionType `json:"contribution_type"`
	Transform        string                 `json:"transform"`
}

type LineageEdge struct {
	ID              string            `json:"id"`
	SourceNodeID    string            `json:"source_node_id"`
	TargetNodeID    string            `json:"target_node_id"`
	TransformType   TransformType     `json:"transform_type"`
	TransformName   string            `json:"transform_name"`
	AffectedRows    int               `json:"affected_rows"`
	AffectedColumns []string          `json:"affected_columns"`
	ColumnLineage   []ColumnLineage   `json:"column_lineage,omitempty"`
}

type LineageGraph struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Nodes     []LineageNode  `json:"nodes"`
	Edges     []LineageEdge  `json:"edges"`
	IsIncremental bool       `json:"is_incremental"`
}

type SnapshotInfo struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	FileSize    int64     `json:"file_size"`
	IsCompressed bool      `json:"is_compressed"`
	NodeCount   int       `json:"node_count"`
	EdgeCount   int       `json:"edge_count"`
}

func NewNodeID() string {
	id, _ := uuid.GenerateUUID()
	return id
}

func NewEdgeID() string {
	id, _ := uuid.GenerateUUID()
	return id
}

func ComputeDatasetFingerprint(ds *datasource.Dataset) string {
	if ds == nil {
		return ""
	}

	h := sha256.New()

	for _, col := range ds.Schema.Columns {
		h.Write([]byte(col.Name))
		h.Write([]byte(col.DataType.String()))
	}

	for _, row := range ds.Rows {
		for i, val := range row.Values {
			if i >= len(ds.Schema.Columns) {
				continue
			}
			if val.IsNull {
				h.Write([]byte("NULL"))
			} else {
				switch val.Type {
				case datasource.TypeInt:
					h.Write([]byte(string(rune(val.IntVal))))
				case datasource.TypeFloat:
					h.Write([]byte(string(rune(val.FloatVal))))
				case datasource.TypeString:
					h.Write([]byte(val.StrVal))
				case datasource.TypeDate:
					h.Write([]byte(val.DateVal.String()))
				case datasource.TypeBool:
					if val.BoolVal {
						h.Write([]byte("true"))
					} else {
						h.Write([]byte("false"))
					}
				default:
					h.Write([]byte(val.Raw))
				}
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

func CreateSourceNode(name string, ds *datasource.Dataset) LineageNode {
	return LineageNode{
		ID:          NewNodeID(),
		Name:        name,
		Type:        NodeTypeSource,
		CreatedAt:   time.Now(),
		RowCount:    len(ds.Rows),
		ColumnCount: len(ds.Schema.Columns),
		Fingerprint: ComputeDatasetFingerprint(ds),
		Columns:     ds.Schema.ColumnNames(),
	}
}

func CreateTransformNode(name string, ds *datasource.Dataset) LineageNode {
	return LineageNode{
		ID:          NewNodeID(),
		Name:        name,
		Type:        NodeTypeTransform,
		CreatedAt:   time.Now(),
		RowCount:    len(ds.Rows),
		ColumnCount: len(ds.Schema.Columns),
		Fingerprint: ComputeDatasetFingerprint(ds),
		Columns:     ds.Schema.ColumnNames(),
	}
}

func CreateOutputNode(name string, ds *datasource.Dataset) LineageNode {
	return LineageNode{
		ID:          NewNodeID(),
		Name:        name,
		Type:        NodeTypeOutput,
		CreatedAt:   time.Now(),
		RowCount:    len(ds.Rows),
		ColumnCount: len(ds.Schema.Columns),
		Fingerprint: ComputeDatasetFingerprint(ds),
		Columns:     ds.Schema.ColumnNames(),
	}
}

func CreateEdge(sourceNodeID, targetNodeID string, transformType TransformType, transformName string, affectedRows int, affectedColumns []string, columnLineage []ColumnLineage) LineageEdge {
	return LineageEdge{
		ID:              NewEdgeID(),
		SourceNodeID:    sourceNodeID,
		TargetNodeID:    targetNodeID,
		TransformType:   transformType,
		TransformName:   transformName,
		AffectedRows:    affectedRows,
		AffectedColumns: affectedColumns,
		ColumnLineage:   columnLineage,
	}
}

func (g *LineageGraph) ToJSON() (string, error) {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func LineageGraphFromJSON(data []byte) (*LineageGraph, error) {
	var g LineageGraph
	err := json.Unmarshal(data, &g)
	if err != nil {
		return nil, err
	}
	return &g, nil
}
