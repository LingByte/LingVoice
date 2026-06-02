package avflow

import (
	"context"
	"testing"
	"time"
)

func TestGraph_NewGraph(t *testing.T) {
	g := NewGraph("test-graph")
	if g.name != "test-graph" {
		t.Errorf("expected 'test-graph', got %s", g.name)
	}
	
	if len(g.components) != 0 {
		t.Errorf("expected 0 components, got %d", len(g.components))
	}
	
	if len(g.edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(g.edges))
	}
}

func TestGraph_AddComponent(t *testing.T) {
	g := NewGraph("test-graph")
	comp := NewMockComponent("comp1", []string{"in"}, []string{"out"})
	
	g.AddComponent(comp)
	
	if len(g.components) != 1 {
		t.Errorf("expected 1 component, got %d", len(g.components))
	}
	
	if _, exists := g.components["comp1"]; !exists {
		t.Error("expected component 'comp1' to be registered")
	}
}

func TestGraph_AddComponent_Nil(t *testing.T) {
	g := NewGraph("test-graph")
	g.AddComponent(nil)
	
	if len(g.components) != 0 {
		t.Errorf("expected 0 components, got %d", len(g.components))
	}
}

func TestGraph_Connect(t *testing.T) {
	g := NewGraph("test-graph")
	
	g.Connect("comp1", "out", "comp2", "in")
	
	if len(g.edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(g.edges))
	}
	
	edge := g.edges[0]
	if edge.FromNode != "comp1" || edge.FromPort != "out" {
		t.Errorf("expected from comp1.out, got %s.%s", edge.FromNode, edge.FromPort)
	}
	
	if edge.ToNode != "comp2" || edge.ToPort != "in" {
		t.Errorf("expected to comp2.in, got %s.%s", edge.ToNode, edge.ToPort)
	}
}

func TestGraph_WithBufferSize(t *testing.T) {
	g := NewGraph("test-graph")
	g.WithBufferSize(128)
	
	if g.bufferSize != 128 {
		t.Errorf("expected buffer size 128, got %d", g.bufferSize)
	}
}

func TestGraph_WithBufferSize_InvalidSize(t *testing.T) {
	g := NewGraph("test-graph")
	originalSize := g.bufferSize
	g.WithBufferSize(-1)
	
	if g.bufferSize != originalSize {
		t.Errorf("expected buffer size unchanged, got %d", g.bufferSize)
	}
}

func TestGraph_Run_EmptyGraph(t *testing.T) {
	g := NewGraph("empty-graph")
	err := g.Run(context.Background())
	
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGraph_Run_SingleComponent(t *testing.T) {
	g := NewGraph("single-comp-graph")
	
	comp := NewMockComponent("comp1", []string{}, []string{})
	comp.processFunc = func(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error {
		return nil
	}
	
	g.AddComponent(comp)
	
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	err := g.Run(ctx)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGraph_Run_InvalidSourceNode(t *testing.T) {
	g := NewGraph("invalid-graph")
	
	comp := NewMockComponent("comp1", []string{}, []string{})
	g.AddComponent(comp)
	g.Connect("nonexistent", "out", "comp1", "in")
	
	err := g.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid source node")
	}
}

func TestGraph_Run_InvalidTargetNode(t *testing.T) {
	g := NewGraph("invalid-graph")
	
	comp := NewMockComponent("comp1", []string{}, []string{})
	g.AddComponent(comp)
	g.Connect("comp1", "out", "nonexistent", "in")
	
	err := g.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid target node")
	}
}

func TestGraph_Chaining(t *testing.T) {
	g := NewGraph("chain-test")
	
	comp1 := NewMockComponent("comp1", []string{}, []string{"out"})
	comp2 := NewMockComponent("comp2", []string{"in"}, []string{})
	
	result := g.AddComponent(comp1).AddComponent(comp2).Connect("comp1", "out", "comp2", "in")
	
	if result != g {
		t.Error("expected chaining to return graph")
	}
	
	if len(g.components) != 2 {
		t.Errorf("expected 2 components, got %d", len(g.components))
	}
	
	if len(g.edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(g.edges))
	}
}

func TestGraph_Run_Nil(t *testing.T) {
	var g *Graph
	err := g.Run(context.Background())
	
	if err == nil {
		t.Fatal("expected error for nil graph")
	}
}

func TestGraph_MultipleConnections(t *testing.T) {
	g := NewGraph("multi-conn-graph")
	
	comp1 := NewMockComponent("comp1", []string{}, []string{"out1", "out2"})
	comp2 := NewMockComponent("comp2", []string{"in"}, []string{})
	comp3 := NewMockComponent("comp3", []string{"in"}, []string{})
	
	g.AddComponent(comp1).AddComponent(comp2).AddComponent(comp3)
	g.Connect("comp1", "out1", "comp2", "in")
	g.Connect("comp1", "out2", "comp3", "in")
	
	if len(g.edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(g.edges))
	}
}

func TestEdge_Structure(t *testing.T) {
	edge := Edge{
		FromNode: "source",
		FromPort: "output",
		ToNode:   "target",
		ToPort:   "input",
	}
	
	if edge.FromNode != "source" {
		t.Errorf("expected FromNode 'source', got %s", edge.FromNode)
	}
	
	if edge.ToNode != "target" {
		t.Errorf("expected ToNode 'target', got %s", edge.ToNode)
	}
}
