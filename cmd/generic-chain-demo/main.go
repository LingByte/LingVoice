// Command generic-chain-demo: GenericChain[I,O] + StringMessageChain (local mock).
//
//	go run ./cmd/generic-chain-demo
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/LingByte/LingVoice/pkg/llm/compose"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func main() {
	fmt.Fprintln(os.Stderr, "demo: generic-chain-demo | GenericChain + StringMessageChain")

	typed := compose.NewGenericChainIO[string]("slug").
		Append(func(_ context.Context, in string) (string, error) {
			return strings.TrimSpace(in), nil
		}).
		Append(func(_ context.Context, in string) (string, error) {
			return strings.ReplaceAll(in, " ", "-"), nil
		})
	slug, err := typed.Invoke(context.Background(), "  hello world  ")
	if err != nil {
		fail(err)
	}
	fmt.Printf("slug: %q\n", slug)

	model := llm.NewFuncModel("mock", func(_ context.Context, msgs []*schema.Message, _ llm.Options) (*schema.Message, error) {
		text := "empty"
		if len(msgs) > 0 && msgs[len(msgs)-1] != nil {
			text = msgs[len(msgs)-1].PlainText()
		}
		return schema.AssistantMessage("echo:"+text, nil), nil
	}, nil)
	msgChain := compose.NewStringMessageChain("echo", &compose.ChatModelStep{Model: model})
	reply, err := msgChain.Invoke(context.Background(), "generic chain")
	if err != nil {
		fail(err)
	}
	fmt.Printf("reply: %s\n", reply)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
