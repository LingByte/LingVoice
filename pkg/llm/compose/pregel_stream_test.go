package compose_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestPregelStreamSuperstep(t *testing.T) {
	streamModel := llm.NewFuncModel("s", nil, func(_ context.Context, _ []*schema.Message, _ llm.Options) (*schema.StreamReader[*schema.Message], error) {
		sr, sw := schema.Pipe[*schema.Message](2)
		go func() {
			defer sw.Close()
			sw.Send(schema.AssistantMessage("tok", nil), nil)
		}()
		return sr, nil
	})

	g := compose.NewGraph("pregel-stream")
	_ = g.AddChatModelNode("a", streamModel)
	_ = g.AddChatModelNode("b", streamModel)
	_ = g.AddFanOutEdges(compose.START, "a", "b")
	join := "join"
	_ = g.AddLambdaNode(join, func(_ context.Context, st *compose.GraphState) error {
		st.LastOutput = schema.AssistantMessage("joined", nil)
		return nil
	})
	_ = g.AddEdge("a", join)
	_ = g.AddEdge("b", join)
	_ = g.AddEdge(join, compose.END)
	_ = g.MarkAnyPredecessorJoin(join)

	cg, err := g.Compile(compose.WithNodeTriggerMode(compose.AnyPredecessor, join))
	if err != nil {
		t.Fatal(err)
	}
	if !cg.HasPregelStreamCapability() {
		t.Fatal("expected pregel stream capability")
	}
	sr, err := cg.StreamMessages(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := schema.CollectMessages(sr)
	if err != nil || msg == nil || msg.Content == "" {
		t.Fatalf("err=%v msg=%v", err, msg)
	}
}
