package lineage

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/data-cleaner/internal/datasource"
	"github.com/hashicorp/go-uuid"
)

var ErrCycleDetected = errors.New("cycle detected in lineage graph")

type LineageTracker struct {
	graph        *LineageGraph
	nodeMap      map[string]*LineageNode
	nodeNameMap  map[string]*LineageNode
	edgeMap      map[string]*LineageEdge
	inEdges      map[string][]*LineageEdge
	outEdges     map[string][]*LineageEdge
	mu           sync.Mutex
	isIncremental bool
}

func NewLineageTracker() *LineageTracker {
	id, _ := uuid.GenerateUUID()
	return &LineageTracker{
		graph: &LineageGraph{
			ID:        id,
			Timestamp: time.Now(),
			Nodes:     []LineageNode{},
			Edges:     []LineageEdge{},
		},
		nodeMap:      make(map[string]*LineageNode),
		nodeNameMap:  make(map[string]*LineageNode),
		edgeMap:      make(map[string]*LineageEdge),
		inEdges:      make(map[string][]*LineageEdge),
		outEdges:     make(map[string][]*LineageEdge),
		isIncremental: false,
	}
}

func NewLineageTrackerIncremental(prevGraph *LineageGraph) *LineageTracker {
	t := NewLineageTracker()
	t.isIncremental = true

	if prevGraph != nil {
		for i := range prevGraph.Nodes {
			node := prevGraph.Nodes[i]
			t.nodeMap[node.ID] = &node
			t.nodeNameMap[node.Name] = &node
			t.graph.Nodes = append(t.graph.Nodes, node)
		}
		for i := range prevGraph.Edges {
			edge := prevGraph.Edges[i]
			t.edgeMap[edge.ID] = &edge
			t.inEdges[edge.TargetNodeID] = append(t.inEdges[edge.TargetNodeID], &edge)
			t.outEdges[edge.SourceNodeID] = append(t.outEdges[edge.SourceNodeID], &edge)
			t.graph.Edges = append(t.graph.Edges, edge)
		}
	}

	return t
}

func (t *LineageTracker) AddSourceNode(name string, ds *datasource.Dataset) (*LineageNode, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.nodeNameMap[name]; ok {
		return existing, nil
	}

	node := CreateSourceNode(name, ds)
	return t.addNodeInternal(&node)
}

func (t *LineageTracker) AddTransformNode(name string, ds *datasource.Dataset) (*LineageNode, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.nodeNameMap[name]; ok {
		return existing, nil
	}

	node := CreateTransformNode(name, ds)
	return t.addNodeInternal(&node)
}

func (t *LineageTracker) AddOutputNode(name string, ds *datasource.Dataset) (*LineageNode, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.nodeNameMap[name]; ok {
		return existing, nil
	}

	node := CreateOutputNode(name, ds)
	return t.addNodeInternal(&node)
}

func (t *LineageTracker) addNodeInternal(node *LineageNode) (*LineageNode, error) {
	t.nodeMap[node.ID] = node
	t.nodeNameMap[node.Name] = node
	t.graph.Nodes = append(t.graph.Nodes, *node)
	return node, nil
}

func (t *LineageTracker) AddEdge(sourceNodeName, targetNodeName string, transformType TransformType, transformName string, affectedRows int, affectedColumns []string, columnLineage []ColumnLineage) (*LineageEdge, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	sourceNode, ok := t.nodeNameMap[sourceNodeName]
	if !ok {
		return nil, fmt.Errorf("source node '%s' not found", sourceNodeName)
	}

	targetNode, ok := t.nodeNameMap[targetNodeName]
	if !ok {
		return nil, fmt.Errorf("target node '%s' not found", targetNodeName)
	}

	if t.wouldCreateCycle(sourceNode.ID, targetNode.ID) {
		return nil, ErrCycleDetected
	}

	edge := CreateEdge(sourceNode.ID, targetNode.ID, transformType, transformName, affectedRows, affectedColumns, columnLineage)

	t.edgeMap[edge.ID] = &edge
	t.inEdges[targetNode.ID] = append(t.inEdges[targetNode.ID], &edge)
	t.outEdges[sourceNode.ID] = append(t.outEdges[sourceNode.ID], &edge)
	t.graph.Edges = append(t.graph.Edges, edge)

	return &edge, nil
}

func (t *LineageTracker) AddEdgeByID(sourceNodeID, targetNodeID string, transformType TransformType, transformName string, affectedRows int, affectedColumns []string, columnLineage []ColumnLineage) (*LineageEdge, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.nodeMap[sourceNodeID]; !ok {
		return nil, fmt.Errorf("source node '%s' not found", sourceNodeID)
	}

	if _, ok := t.nodeMap[targetNodeID]; !ok {
		return nil, fmt.Errorf("target node '%s' not found", targetNodeID)
	}

	if t.wouldCreateCycle(sourceNodeID, targetNodeID) {
		return nil, ErrCycleDetected
	}

	edge := CreateEdge(sourceNodeID, targetNodeID, transformType, transformName, affectedRows, affectedColumns, columnLineage)

	t.edgeMap[edge.ID] = &edge
	t.inEdges[targetNodeID] = append(t.inEdges[targetNodeID], &edge)
	t.outEdges[sourceNodeID] = append(t.outEdges[sourceNodeID], &edge)
	t.graph.Edges = append(t.graph.Edges, edge)

	return &edge, nil
}

func (t *LineageTracker) wouldCreateCycle(sourceID, targetID string) bool {
	visited := make(map[string]bool)
	return t.dfsCycleCheck(targetID, sourceID, visited)
}

func (t *LineageTracker) dfsCycleCheck(current, target string, visited map[string]bool) bool {
	if current == target {
		return true
	}
	visited[current] = true

	for _, edge := range t.outEdges[current] {
		if !visited[edge.TargetNodeID] {
			if t.dfsCycleCheck(edge.TargetNodeID, target, visited) {
				return true
			}
		}
	}
	return false
}

func (t *LineageTracker) ValidateDAG() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	visited := make(map[string]int)

	for _, node := range t.graph.Nodes {
		if visited[node.ID] == 0 {
			if t.dfsCycleCheckFromNode(node.ID, visited) {
				return ErrCycleDetected
			}
		}
	}
	return nil
}

func (t *LineageTracker) dfsCycleCheckFromNode(nodeID string, visited map[string]int) bool {
	visited[nodeID] = 1

	for _, edge := range t.outEdges[nodeID] {
		if visited[edge.TargetNodeID] == 1 {
			return true
		}
		if visited[edge.TargetNodeID] == 0 {
			if t.dfsCycleCheckFromNode(edge.TargetNodeID, visited) {
				return true
			}
		}
	}

	visited[nodeID] = 2
	return false
}

func (t *LineageTracker) GetNodeByID(id string) (*LineageNode, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	node, ok := t.nodeMap[id]
	return node, ok
}

func (t *LineageTracker) GetNodeByName(name string) (*LineageNode, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	node, ok := t.nodeNameMap[name]
	return node, ok
}

func (t *LineageTracker) GetInEdges(nodeID string) []*LineageEdge {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.inEdges[nodeID]
}

func (t *LineageTracker) GetOutEdges(nodeID string) []*LineageEdge {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.outEdges[nodeID]
}

func (t *LineageTracker) GetGraph() *LineageGraph {
	t.mu.Lock()
	defer t.mu.Unlock()

	graphCopy := *t.graph
	graphCopy.IsIncremental = t.isIncremental
	return &graphCopy
}

func (t *LineageTracker) GetSourceNodes() []*LineageNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	var sources []*LineageNode
	for _, node := range t.graph.Nodes {
		if node.Type == NodeTypeSource {
			sources = append(sources, t.nodeMap[node.ID])
		}
	}
	return sources
}

func (t *LineageTracker) GetOutputNodes() []*LineageNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	var outputs []*LineageNode
	for _, node := range t.graph.Nodes {
		if node.Type == NodeTypeOutput {
			outputs = append(outputs, t.nodeMap[node.ID])
		}
	}
	return outputs
}

func (t *LineageTracker) GetTransformNodes() []*LineageNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	var transforms []*LineageNode
	for _, node := range t.graph.Nodes {
		if node.Type == NodeTypeTransform {
			transforms = append(transforms, t.nodeMap[node.ID])
		}
	}
	return transforms
}

func GenerateColumnLineage(sourceDS, targetDS *datasource.Dataset, transformName string) []ColumnLineage {
	if sourceDS == nil || targetDS == nil {
		return nil
	}

	var lineage []ColumnLineage

	for _, targetCol := range targetDS.Schema.Columns {
		sourceIdx := sourceDS.Schema.ColumnIndex(targetCol.Name)
		if sourceIdx >= 0 {
			lineage = append(lineage, ColumnLineage{
				TargetColumn:     targetCol.Name,
				SourceColumns:    []string{targetCol.Name},
				ContributionType: ContributionCopy,
				Transform:        transformName,
			})
		}
	}

	return lineage
}

func GenerateMergeColumnLineage(sourceDatasets []*datasource.Dataset, targetDS *datasource.Dataset, transformName string) []ColumnLineage {
	if len(sourceDatasets) == 0 || targetDS == nil {
		return nil
	}

	var lineage []ColumnLineage

	for _, targetCol := range targetDS.Schema.Columns {
		var sourceCols []string
		for _, srcDS := range sourceDatasets {
			if srcDS.Schema.ColumnIndex(targetCol.Name) >= 0 {
				sourceCols = append(sourceCols, targetCol.Name)
			}
		}

		if len(sourceCols) > 0 {
			contribType := ContributionUnion
			if len(sourceCols) == 1 {
				contribType = ContributionCopy
			}
			lineage = append(lineage, ColumnLineage{
				TargetColumn:     targetCol.Name,
				SourceColumns:    sourceCols,
				ContributionType: contribType,
				Transform:        transformName,
			})
		}
	}

	return lineage
}
