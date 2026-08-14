package lineage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	boxTree    = "├── "
	boxLast    = "└── "
	boxVert    = "│   "
	boxSpace   = "    "
)

func PrintTree(graph *LineageGraph, maxDepth int) string {
	q := NewGraphQuery(graph)
	sources := q.GetSourceNodes()

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("Lineage Graph: %s\n\n", graph.ID))

	visited := make(map[string]bool)
	for i, source := range sources {
		isLast := i == len(sources)-1
		printNodeTree(q, source, "", isLast, 0, maxDepth, visited, &buf)
	}

	return buf.String()
}

func printNodeTree(q *GraphQuery, node *LineageNode, prefix string, isLast bool, depth int, maxDepth int, visited map[string]bool, buf *bytes.Buffer) {
	if visited[node.ID] {
		return
	}
	visited[node.ID] = true

	if maxDepth > 0 && depth >= maxDepth {
		return
	}

	connector := boxTree
	if isLast {
		connector = boxLast
	}

	nodeLabel := fmtNodeLabel(node)
	buf.WriteString(fmt.Sprintf("%s%s%s\n", prefix, connector, nodeLabel))

	outEdges := q.GetOutEdges(node.ID)
	for i, edge := range outEdges {
		childNode, ok := q.GetNodeByID(edge.TargetNodeID)
		if !ok {
			continue
		}

		childIsLast := i == len(outEdges)-1
		childPrefix := prefix
		if isLast {
			childPrefix += boxSpace
		} else {
			childPrefix += boxVert
		}

		printNodeTree(q, childNode, childPrefix, childIsLast, depth+1, maxDepth, visited, buf)
	}
}

func fmtNodeLabel(node *LineageNode) string {
	typeIcon := "[SRC]"
	if node.Type == NodeTypeTransform {
		typeIcon = "[TFM]"
	} else if node.Type == NodeTypeOutput {
		typeIcon = "[OUT]"
	}
	return fmt.Sprintf("%s %s (%d rows, %d cols)", typeIcon, node.Name, node.RowCount, node.ColumnCount)
}

func PrintColumnTrace(results []ColumnTraceResult) string {
	var buf bytes.Buffer

	if len(results) == 0 {
		buf.WriteString("No column lineage found.\n")
		return buf.String()
	}

	buf.WriteString("Column Lineage Trace:\n")
	buf.WriteString(strings.Repeat("=", 60) + "\n")

	for i, r := range results {
		buf.WriteString(fmt.Sprintf("\nTrace %d:\n", i+1))
		buf.WriteString(fmt.Sprintf("  Column: %s\n", r.ColumnName))
		buf.WriteString(fmt.Sprintf("  Source Columns: %v\n", r.SourceColumns))
		buf.WriteString(fmt.Sprintf("  Path: %s\n", strings.Join(r.Path, " -> ")))
		if len(r.Transforms) > 0 {
			buf.WriteString(fmt.Sprintf("  Transforms: %s\n", strings.Join(r.Transforms, " -> ")))
		}
		buf.WriteString(fmt.Sprintf("  Contribution: %s\n", r.ContributionType))
	}

	return buf.String()
}

func PrintImpactAnalysis(result *ImpactAnalysisResult) string {
	var buf bytes.Buffer

	buf.WriteString("Impact Analysis Report:\n")
	buf.WriteString(strings.Repeat("=", 60) + "\n\n")

	buf.WriteString("Schema Changes:\n")
	for _, sc := range result.SchemaChanges {
		buf.WriteString(fmt.Sprintf("  - [%s] %s: %s\n", sc.ChangeType, sc.Column, sc.Description))
	}

	buf.WriteString(fmt.Sprintf("\nAffected Nodes (%d):\n", len(result.AffectedNodes)))
	for _, node := range result.AffectedNodes {
		buf.WriteString(fmt.Sprintf("  - %s [%s] (%d rows, %d cols)\n", node.Name, node.Type, node.RowCount, node.ColumnCount))
	}

	buf.WriteString(fmt.Sprintf("\nAffected Columns (%d):\n", len(result.AffectedColumns)))
	for _, col := range result.AffectedColumns {
		buf.WriteString(fmt.Sprintf("  - %s\n", col))
	}

	return buf.String()
}

func PrintHistory(snapshots []SnapshotInfo) string {
	var buf bytes.Buffer

	if len(snapshots) == 0 {
		buf.WriteString("No lineage snapshots found.\n")
		return buf.String()
	}

	buf.WriteString(fmt.Sprintf("Lineage Snapshots (%d total):\n", len(snapshots)))
	buf.WriteString(strings.Repeat("-", 80) + "\n")
	buf.WriteString(fmt.Sprintf("%-20s %-10s %-12s %-12s %-10s %s\n",
		"Timestamp", "ID", "Nodes", "Edges", "Size", "Compressed"))
	buf.WriteString(strings.Repeat("-", 80) + "\n")

	for _, s := range snapshots {
		compressed := "No"
		if s.IsCompressed {
			compressed = "Yes"
		}
		sizeStr := fmt.Sprintf("%.1f KB", float64(s.FileSize)/1024)
		if s.FileSize > 1024*1024 {
			sizeStr = fmt.Sprintf("%.1f MB", float64(s.FileSize)/1024/1024)
		}
		buf.WriteString(fmt.Sprintf("%-20s %-10s %-12d %-12d %-10s %s\n",
			s.Timestamp.Format("2006-01-02 15:04:05"),
			s.ID,
			s.NodeCount,
			s.EdgeCount,
			sizeStr,
			compressed))
	}

	return buf.String()
}

func PrintDiff(diff *LineageDiff) string {
	var buf bytes.Buffer

	buf.WriteString("Lineage Diff Report\n")
	buf.WriteString(strings.Repeat("=", 60) + "\n\n")

	hasChanges := false

	if len(diff.AddedNodes) > 0 {
		hasChanges = true
		buf.WriteString(fmt.Sprintf("Added Nodes (%d):\n", len(diff.AddedNodes)))
		for _, node := range diff.AddedNodes {
			buf.WriteString(fmt.Sprintf("  + %s [%s] (%d rows, %d cols)\n", node.Name, node.Type, node.RowCount, node.ColumnCount))
		}
		buf.WriteString("\n")
	}

	if len(diff.RemovedNodes) > 0 {
		hasChanges = true
		buf.WriteString(fmt.Sprintf("Removed Nodes (%d):\n", len(diff.RemovedNodes)))
		for _, node := range diff.RemovedNodes {
			buf.WriteString(fmt.Sprintf("  - %s [%s]\n", node.Name, node.Type))
		}
		buf.WriteString("\n")
	}

	if len(diff.ModifiedNodes) > 0 {
		hasChanges = true
		buf.WriteString(fmt.Sprintf("Modified Nodes (%d):\n", len(diff.ModifiedNodes)))
		for _, mn := range diff.ModifiedNodes {
			buf.WriteString(fmt.Sprintf("  ~ %s:\n", mn.NewNode.Name))
			for _, c := range mn.Changes {
				buf.WriteString(fmt.Sprintf("      %s: %v -> %v\n", c.Field, c.OldValue, c.NewValue))
			}
		}
		buf.WriteString("\n")
	}

	if len(diff.AddedEdges) > 0 {
		hasChanges = true
		buf.WriteString(fmt.Sprintf("Added Edges (%d):\n", len(diff.AddedEdges)))
		for _, edge := range diff.AddedEdges {
			buf.WriteString(fmt.Sprintf("  + [%s] -> [%s] (%s)\n", edge.SourceNodeID[:8], edge.TargetNodeID[:8], edge.TransformType))
		}
		buf.WriteString("\n")
	}

	if len(diff.RemovedEdges) > 0 {
		hasChanges = true
		buf.WriteString(fmt.Sprintf("Removed Edges (%d):\n", len(diff.RemovedEdges)))
		for _, edge := range diff.RemovedEdges {
			buf.WriteString(fmt.Sprintf("  - [%s] -> [%s] (%s)\n", edge.SourceNodeID[:8], edge.TargetNodeID[:8], edge.TransformType))
		}
		buf.WriteString("\n")
	}

	if len(diff.ModifiedEdges) > 0 {
		hasChanges = true
		buf.WriteString(fmt.Sprintf("Modified Edges (%d):\n", len(diff.ModifiedEdges)))
		for _, me := range diff.ModifiedEdges {
			buf.WriteString(fmt.Sprintf("  ~ [%s] -> [%s]:\n", me.NewEdge.SourceNodeID[:8], me.NewEdge.TargetNodeID[:8]))
			for _, c := range me.Changes {
				buf.WriteString(fmt.Sprintf("      %s: %v -> %v\n", c.Field, c.OldValue, c.NewValue))
			}
		}
		buf.WriteString("\n")
	}

	if !hasChanges {
		buf.WriteString("No changes detected between the two lineage graphs.\n")
	}

	return buf.String()
}

func ToDOT(graph *LineageGraph) string {
	var buf bytes.Buffer

	buf.WriteString("digraph LineageGraph {\n")
	buf.WriteString("  rankdir=TB;\n")
	buf.WriteString("  node [shape=box, style=filled, fontname=Arial];\n")
	buf.WriteString("  edge [fontname=Arial];\n\n")

	nodeNameMap := make(map[string]string)
	for i, node := range graph.Nodes {
		nodeVar := fmt.Sprintf("node%d", i)
		nodeNameMap[node.ID] = nodeVar

		fillColor := "#e1f5fe"
		if node.Type == NodeTypeTransform {
			fillColor = "#fff9c4"
		} else if node.Type == NodeTypeOutput {
			fillColor = "#c8e6c9"
		}

		label := fmt.Sprintf("%s\\n%s\\n%d rows, %d cols",
			node.Name, strings.ToUpper(string(node.Type)), node.RowCount, node.ColumnCount)
		buf.WriteString(fmt.Sprintf("  %s [label=\"%s\", fillcolor=\"%s\"];\n", nodeVar, label, fillColor))
	}

	buf.WriteString("\n")

	for _, edge := range graph.Edges {
		srcVar := nodeNameMap[edge.SourceNodeID]
		tgtVar := nodeNameMap[edge.TargetNodeID]
		if srcVar != "" && tgtVar != "" {
			label := string(edge.TransformType)
			if edge.TransformName != "" {
				label = edge.TransformName
			}
			buf.WriteString(fmt.Sprintf("  %s -> %s [label=\"%s\"];\n", srcVar, tgtVar, label))
		}
	}

	buf.WriteString("}\n")
	return buf.String()
}

func ToJSON(graph *LineageGraph, pretty bool) (string, error) {
	if pretty {
		data, err := json.MarshalIndent(graph, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func DiffToJSON(diff *LineageDiff, pretty bool) (string, error) {
	if pretty {
		data, err := json.MarshalIndent(diff, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := json.Marshal(diff)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
