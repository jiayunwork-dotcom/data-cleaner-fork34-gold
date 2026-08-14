package lineage

import (
	"fmt"
)

type TraceResult struct {
	Node    *LineageNode
	Path    []string
	Depth   int
	Edge    *LineageEdge
}

type ColumnTraceResult struct {
	ColumnName string
	SourceColumns []string
	Path       []string
	Transforms []string
	ContributionType ColumnContributionType
}

type ImpactAnalysisResult struct {
	AffectedNodes     []*LineageNode
	AffectedColumns   []string
	SchemaChanges     []SchemaChange
}

type SchemaChange struct {
	Column      string
	ChangeType  string
	Description string
}

type GraphQuery struct {
	graph    *LineageGraph
	nodeMap  map[string]*LineageNode
	nameMap  map[string]*LineageNode
	inEdges  map[string][]*LineageEdge
	outEdges map[string][]*LineageEdge
}

func NewGraphQuery(graph *LineageGraph) *GraphQuery {
	q := &GraphQuery{
		graph:    graph,
		nodeMap:  make(map[string]*LineageNode),
		nameMap:  make(map[string]*LineageNode),
		inEdges:  make(map[string][]*LineageEdge),
		outEdges: make(map[string][]*LineageEdge),
	}

	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		q.nodeMap[node.ID] = node
		q.nameMap[node.Name] = node
	}

	for i := range graph.Edges {
		edge := &graph.Edges[i]
		q.inEdges[edge.TargetNodeID] = append(q.inEdges[edge.TargetNodeID], edge)
		q.outEdges[edge.SourceNodeID] = append(q.outEdges[edge.SourceNodeID], edge)
	}

	return q
}

func (q *GraphQuery) ForwardTrace(nodeName string) ([]TraceResult, error) {
	node, ok := q.nameMap[nodeName]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", nodeName)
	}

	var results []TraceResult
	visited := make(map[string]bool)

	q.dfsForward(node.ID, []string{node.Name}, 0, visited, &results)

	return results, nil
}

func (q *GraphQuery) dfsForward(nodeID string, path []string, depth int, visited map[string]bool, results *[]TraceResult) {
	if visited[nodeID] {
		return
	}
	visited[nodeID] = true

	node := q.nodeMap[nodeID]
	*results = append(*results, TraceResult{
		Node:  node,
		Path:  append([]string{}, path...),
		Depth: depth,
	})

	for _, edge := range q.outEdges[nodeID] {
		newPath := append(append([]string{}, path...), q.nodeMap[edge.TargetNodeID].Name)
		q.dfsForward(edge.TargetNodeID, newPath, depth+1, visited, results)
	}
}

func (q *GraphQuery) BackwardTrace(nodeName string) ([]TraceResult, error) {
	node, ok := q.nameMap[nodeName]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", nodeName)
	}

	var results []TraceResult
	visited := make(map[string]bool)

	q.dfsBackward(node.ID, []string{node.Name}, 0, visited, &results)

	return results, nil
}

func (q *GraphQuery) dfsBackward(nodeID string, path []string, depth int, visited map[string]bool, results *[]TraceResult) {
	if visited[nodeID] {
		return
	}
	visited[nodeID] = true

	node := q.nodeMap[nodeID]
	*results = append(*results, TraceResult{
		Node:  node,
		Path:  append([]string{}, path...),
		Depth: depth,
	})

	for _, edge := range q.inEdges[nodeID] {
		newPath := append(append([]string{}, path...), q.nodeMap[edge.SourceNodeID].Name)
		q.dfsBackward(edge.SourceNodeID, newPath, depth+1, visited, results)
	}
}

func (q *GraphQuery) TraceColumn(columnName string, startNodeName string) ([]ColumnTraceResult, error) {
	startNode, ok := q.nameMap[startNodeName]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", startNodeName)
	}

	var results []ColumnTraceResult
	visited := make(map[string]bool)

	q.dfsColumnTrace(startNode.ID, columnName, []string{startNode.Name}, []string{}, visited, &results)

	return results, nil
}

func (q *GraphQuery) dfsColumnTrace(nodeID string, columnName string, path []string, transforms []string, visited map[string]bool, results *[]ColumnTraceResult) {
	key := fmt.Sprintf("%s:%s", nodeID, columnName)
	if visited[key] {
		return
	}
	visited[key] = true

	node := q.nodeMap[nodeID]
	if node.Type == NodeTypeSource {
		*results = append(*results, ColumnTraceResult{
			ColumnName:  columnName,
			SourceColumns: []string{columnName},
			Path:        append([]string{}, path...),
			Transforms:  append([]string{}, transforms...),
			ContributionType: ContributionCopy,
		})
		return
	}

	for _, edge := range q.inEdges[nodeID] {
		for _, cl := range edge.ColumnLineage {
			if cl.TargetColumn == columnName {
				for _, srcCol := range cl.SourceColumns {
					newPath := append(append([]string{}, path...), q.nodeMap[edge.SourceNodeID].Name)
					newTransforms := append(append([]string{}, transforms...), cl.Transform)
					q.dfsColumnTrace(edge.SourceNodeID, srcCol, newPath, newTransforms, visited, results)
				}
			}
		}
	}
}

func (q *GraphQuery) ImpactAnalysis(sourceNodeName string, schemaChanges []SchemaChange) (*ImpactAnalysisResult, error) {
	_, ok := q.nameMap[sourceNodeName]
	if !ok {
		return nil, fmt.Errorf("source node not found: %s", sourceNodeName)
	}

	forwardTrace, err := q.ForwardTrace(sourceNodeName)
	if err != nil {
		return nil, err
	}

	hasWildcard := false
	affectedColumnsMap := make(map[string]bool)
	for _, sc := range schemaChanges {
		if sc.Column == "*" {
			hasWildcard = true
		}
		affectedColumnsMap[sc.Column] = true
	}

	var affectedNodes []*LineageNode
	allAffectedColumns := make(map[string]bool)

	for _, r := range forwardTrace {
		node := r.Node
		nodeAffected := false

		if hasWildcard {
			nodeAffected = true
			for _, col := range node.Columns {
				allAffectedColumns[col] = true
			}
		} else {
			for col := range affectedColumnsMap {
				for _, nodeCol := range node.Columns {
					if nodeCol == col {
						nodeAffected = true
						allAffectedColumns[col] = true
					}
				}
			}

			for _, edge := range q.inEdges[node.ID] {
				for _, cl := range edge.ColumnLineage {
					for _, srcCol := range cl.SourceColumns {
						if affectedColumnsMap[srcCol] {
							nodeAffected = true
							allAffectedColumns[cl.TargetColumn] = true
						}
					}
				}
			}
		}

		if nodeAffected {
			affectedNodes = append(affectedNodes, node)
		}
	}

	var affectedColumns []string
	for col := range allAffectedColumns {
		affectedColumns = append(affectedColumns, col)
	}

	return &ImpactAnalysisResult{
		AffectedNodes:   affectedNodes,
		AffectedColumns: affectedColumns,
		SchemaChanges:   schemaChanges,
	}, nil
}

func (q *GraphQuery) GetNodeByName(name string) (*LineageNode, bool) {
	node, ok := q.nameMap[name]
	return node, ok
}

func (q *GraphQuery) GetNodeByID(id string) (*LineageNode, bool) {
	node, ok := q.nodeMap[id]
	return node, ok
}

func (q *GraphQuery) GetInEdges(nodeID string) []*LineageEdge {
	return q.inEdges[nodeID]
}

func (q *GraphQuery) GetOutEdges(nodeID string) []*LineageEdge {
	return q.outEdges[nodeID]
}

func (q *GraphQuery) GetSourceNodes() []*LineageNode {
	var sources []*LineageNode
	for _, node := range q.graph.Nodes {
		if node.Type == NodeTypeSource {
			sources = append(sources, &node)
		}
	}
	return sources
}

func (q *GraphQuery) GetOutputNodes() []*LineageNode {
	var outputs []*LineageNode
	for _, node := range q.graph.Nodes {
		if node.Type == NodeTypeOutput {
			outputs = append(outputs, &node)
		}
	}
	return outputs
}
