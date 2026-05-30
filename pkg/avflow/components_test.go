package avflow

import (
	"context"
	"testing"
)

func TestBaseComponent_ID(t *testing.T) {
	comp := NewBaseComponent("test-id", "test-type", []string{"in"}, []string{"out"})
	if comp.ID() != "test-id" {
		t.Errorf("expected 'test-id', got %s", comp.ID())
	}
}

func TestBaseComponent_Type(t *testing.T) {
	comp := NewBaseComponent("test-id", "test-type", []string{"in"}, []string{"out"})
	if comp.Type() != "test-type" {
		t.Errorf("expected 'test-type', got %s", comp.Type())
	}
}

func TestBaseComponent_Inputs(t *testing.T) {
	inputs := []string{"in1", "in2"}
	comp := NewBaseComponent("test-id", "test-type", inputs, []string{"out"})
	
	retrievedInputs := comp.Inputs()
	if len(retrievedInputs) != 2 {
		t.Errorf("expected 2 inputs, got %d", len(retrievedInputs))
	}
}

func TestBaseComponent_Outputs(t *testing.T) {
	outputs := []string{"out1", "out2"}
	comp := NewBaseComponent("test-id", "test-type", []string{"in"}, outputs)
	
	retrievedOutputs := comp.Outputs()
	if len(retrievedOutputs) != 2 {
		t.Errorf("expected 2 outputs, got %d", len(retrievedOutputs))
	}
}

func TestPacket_NewPacket(t *testing.T) {
	pkt := NewPacket(PacketTypeText, "test data")
	
	if pkt.Type != PacketTypeText {
		t.Errorf("expected PacketTypeText, got %v", pkt.Type)
	}
	
	if pkt.Data != "test data" {
		t.Errorf("expected 'test data', got %v", pkt.Data)
	}
	
	if pkt.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	
	if pkt.Metadata == nil {
		t.Error("expected non-nil metadata map")
	}
}

func TestPacket_WithMetadata(t *testing.T) {
	pkt := NewPacket(PacketTypeAudio, []byte{1, 2, 3})
	pkt.Metadata["sample_rate"] = 16000
	pkt.Metadata["channels"] = 1
	
	if pkt.Metadata["sample_rate"] != 16000 {
		t.Errorf("expected sample_rate 16000, got %v", pkt.Metadata["sample_rate"])
	}
	
	if pkt.Metadata["channels"] != 1 {
		t.Errorf("expected channels 1, got %v", pkt.Metadata["channels"])
	}
}

func TestPacket_DifferentTypes(t *testing.T) {
	types := []PacketType{
		PacketTypeAudio,
		PacketTypeVideo,
		PacketTypeText,
		PacketTypeMessage,
		PacketTypeControl,
		PacketTypeGeneric,
	}
	
	for _, pktType := range types {
		pkt := NewPacket(pktType, "data")
		if pkt.Type != pktType {
			t.Errorf("expected %v, got %v", pktType, pkt.Type)
		}
	}
}

type MockComponent struct {
	BaseComponent
	processFunc func(context.Context, map[string]<-chan *Packet, map[string]chan<- *Packet) error
}

func NewMockComponent(id string, inputs, outputs []string) *MockComponent {
	return &MockComponent{
		BaseComponent: NewBaseComponent(id, "mock", inputs, outputs),
		processFunc: func(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error {
			return nil
		},
	}
}

func (mc *MockComponent) Process(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error {
	return mc.processFunc(ctx, inputs, outputs)
}

func TestMockComponent_Process(t *testing.T) {
	comp := NewMockComponent("test", []string{"in"}, []string{"out"})
	
	inputs := make(map[string]<-chan *Packet)
	outputs := make(map[string]chan<- *Packet)
	
	err := comp.Process(context.Background(), inputs, outputs)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMockComponent_WithCustomProcess(t *testing.T) {
	comp := NewMockComponent("test", []string{"in"}, []string{"out"})
	
	called := false
	comp.processFunc = func(ctx context.Context, inputs map[string]<-chan *Packet, outputs map[string]chan<- *Packet) error {
		called = true
		return nil
	}
	
	inputs := make(map[string]<-chan *Packet)
	outputs := make(map[string]chan<- *Packet)
	
	comp.Process(context.Background(), inputs, outputs)
	
	if !called {
		t.Error("expected processFunc to be called")
	}
}

func TestComponent_Interface(t *testing.T) {
	var _ Component = (*MockComponent)(nil)
}
