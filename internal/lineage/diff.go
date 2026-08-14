package lineage

type LineageDiff struct {
	AddedNodes   []LineageNode
	RemovedNodes []LineageNode
	ModifiedNodes []ModifiedNode
	AddedEdges   []LineageEdge
	RemovedEdges []LineageEdge
	ModifiedEdges []ModifiedEdge
}

type ModifiedNode struct {
	OldNode LineageNode
	NewNode LineageNode
	Changes []NodeChange
}

type NodeChange struct {
	Field      string
	OldValue   interface{}
	NewValue   interface{}
}

type ModifiedEdge struct {
	OldEdge LineageEdge
	NewEdge LineageEdge
	Changes []EdgeChange
}

type EdgeChange struct {
	Field    string
	OldValue interface{}
	NewValue interface{}
}

func CompareGraphs(oldGraph, newGraph *LineageGraph) *LineageDiff {
	diff := &LineageDiff{}

	oldNodeMap := make(map[string]*LineageNode)
	oldNodeNameMap := make(map[string]*LineageNode)
	for i := range oldGraph.Nodes {
		node := &oldGraph.Nodes[i]
		oldNodeMap[node.ID] = node
		oldNodeNameMap[node.Name] = node
	}

	newNodeMap := make(map[string]*LineageNode)
	newNodeNameMap := make(map[string]*LineageNode)
	for i := range newGraph.Nodes {
		node := &newGraph.Nodes[i]
		newNodeMap[node.ID] = node
		newNodeNameMap[node.Name] = node
	}

	for name, newNode := range newNodeNameMap {
		if oldNode, ok := oldNodeNameMap[name]; ok {
			changes := compareNodes(oldNode, newNode)
			if len(changes) > 0 {
				diff.ModifiedNodes = append(diff.ModifiedNodes, ModifiedNode{
					OldNode: *oldNode,
					NewNode: *newNode,
					Changes: changes,
				})
			}
		} else {
			diff.AddedNodes = append(diff.AddedNodes, *newNode)
		}
	}

	for name, oldNode := range oldNodeNameMap {
		if _, ok := newNodeNameMap[name]; !ok {
			diff.RemovedNodes = append(diff.RemovedNodes, *oldNode)
		}
	}

	oldEdgeKeyMap := make(map[string]*LineageEdge)
	for i := range oldGraph.Edges {
		edge := &oldGraph.Edges[i]
		key := getEdgeKey(edge, oldNodeMap)
		oldEdgeKeyMap[key] = edge
	}

	newEdgeKeyMap := make(map[string]*LineageEdge)
	for i := range newGraph.Edges {
		edge := &newGraph.Edges[i]
		key := getEdgeKey(edge, newNodeMap)
		newEdgeKeyMap[key] = edge
	}

	for key, newEdge := range newEdgeKeyMap {
		if oldEdge, ok := oldEdgeKeyMap[key]; ok {
			changes := compareEdges(oldEdge, newEdge)
			if len(changes) > 0 {
				diff.ModifiedEdges = append(diff.ModifiedEdges, ModifiedEdge{
					OldEdge: *oldEdge,
					NewEdge: *newEdge,
					Changes: changes,
				})
			}
		} else {
			diff.AddedEdges = append(diff.AddedEdges, *newEdge)
		}
	}

	for key, oldEdge := range oldEdgeKeyMap {
		if _, ok := newEdgeKeyMap[key]; !ok {
			diff.RemovedEdges = append(diff.RemovedEdges, *oldEdge)
		}
	}

	return diff
}

func getEdgeKey(edge *LineageEdge, nodeMap map[string]*LineageNode) string {
	sourceName := ""
	targetName := ""
	if node, ok := nodeMap[edge.SourceNodeID]; ok {
		sourceName = node.Name
	}
	if node, ok := nodeMap[edge.TargetNodeID]; ok {
		targetName = node.Name
	}
	return sourceName + "->" + targetName + ":" + string(edge.TransformType)
}

func compareNodes(oldNode, newNode *LineageNode) []NodeChange {
	var changes []NodeChange

	if oldNode.RowCount != newNode.RowCount {
		changes = append(changes, NodeChange{
			Field:    "RowCount",
			OldValue: oldNode.RowCount,
			NewValue: newNode.RowCount,
		})
	}

	if oldNode.ColumnCount != newNode.ColumnCount {
		changes = append(changes, NodeChange{
			Field:    "ColumnCount",
			OldValue: oldNode.ColumnCount,
			NewValue: newNode.ColumnCount,
		})
	}

	if oldNode.Fingerprint != newNode.Fingerprint {
		changes = append(changes, NodeChange{
			Field:    "Fingerprint",
			OldValue: oldNode.Fingerprint[:16] + "...",
			NewValue: newNode.Fingerprint[:16] + "...",
		})
	}

	if len(oldNode.Columns) != len(newNode.Columns) {
		changes = append(changes, NodeChange{
			Field:    "Columns",
			OldValue: oldNode.Columns,
			NewValue: newNode.Columns,
		})
	} else {
		for i, col := range oldNode.Columns {
			if newNode.Columns[i] != col {
				changes = append(changes, NodeChange{
					Field:    "Columns",
					OldValue: oldNode.Columns,
					NewValue: newNode.Columns,
				})
				break
			}
		}
	}

	return changes
}

func compareEdges(oldEdge, newEdge *LineageEdge) []EdgeChange {
	var changes []EdgeChange

	if oldEdge.AffectedRows != newEdge.AffectedRows {
		changes = append(changes, EdgeChange{
			Field:    "AffectedRows",
			OldValue: oldEdge.AffectedRows,
			NewValue: newEdge.AffectedRows,
		})
	}

	if len(oldEdge.AffectedColumns) != len(newEdge.AffectedColumns) {
		changes = append(changes, EdgeChange{
			Field:    "AffectedColumns",
			OldValue: oldEdge.AffectedColumns,
			NewValue: newEdge.AffectedColumns,
		})
	} else {
		for i, col := range oldEdge.AffectedColumns {
			if newEdge.AffectedColumns[i] != col {
				changes = append(changes, EdgeChange{
					Field:    "AffectedColumns",
					OldValue: oldEdge.AffectedColumns,
					NewValue: newEdge.AffectedColumns,
				})
				break
			}
		}
	}

	return changes
}
