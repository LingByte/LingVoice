// Package demo provides shared helpers for cmd examples.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/anthropic"
	"github.com/LingByte/LingVoice/pkg/llm/instrument"
	"github.com/LingByte/LingVoice/pkg/llm/metrics"
	"github.com/LingByte/LingVoice/pkg/llm/openai"
	"github.com/LingByte/LingVoice/pkg/protocol/llm"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

// Flags common across cmd examples.
type Flags struct {
	Provider string
	Model    string
	BaseURL  string
	Prompt   string
	System   string
	Stream   bool
	Metrics  bool
}

// RegisterFlags adds standard CLI flags to fs.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Provider, "provider", "openai", "openai or anthropic")
	fs.StringVar(&f.Model, "model", "", "override model id")
	fs.StringVar(&f.BaseURL, "base-url", "", "optional API base URL")
	fs.StringVar(&f.Prompt, "prompt", "", "user message")
	fs.StringVar(&f.System, "system", "You are a concise technical assistant.", "system prompt")
	fs.BoolVar(&f.Stream, "stream", false, "use streaming")
	fs.BoolVar(&f.Metrics, "metrics", true, "record async metrics")
	return f
}

// BuildModel creates a provider ChatModel from flags.
func BuildModel(provider, model, baseURL string) (llm.ChatModel, string, error) {
	switch strings.ToLower(provider) {
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, "", errors.New("OPENAI_API_KEY or DASHSCOPE_API_KEY is not set")
		}
		cfg := openai.Config{APIKey: key, Model: model, BaseURL: baseURL}
		m, err := openai.NewChatModel(cfg)
		if err != nil {
			return nil, "", err
		}
		return m, m.Name(), nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, "", errors.New("ANTHROPIC_API_KEY is not set")
		}
		cfg := anthropic.Config{APIKey: key, Model: model, BaseURL: baseURL}
		m, err := anthropic.NewChatModel(cfg)
		if err != nil {
			return nil, "", err
		}
		return m, m.Name(), nil
	default:
		return nil, "", fmt.Errorf("unknown provider %q", provider)
	}
}

// WrapMetrics optionally instruments the model.
func WrapMetrics(inner llm.ChatModel, store *metrics.MemoryStore, enable bool) llm.ChatModel {
	if !enable || inner == nil {
		return inner
	}
	return instrument.Wrap(inner, metrics.NewHandler(store))
}

// Context returns a default timeout context and metrics slot.
func Context(timeout time.Duration) (context.Context, context.CancelFunc, *metrics.RunSlot) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx, slot := metrics.WithRunSlot(ctx)
	return ctx, cancel, slot
}

// RunStream prints streaming chunks to stdout.
func RunStream(ctx context.Context, chat llm.ChatModel, msgs []*schema.Message, opts ...llm.Option) error {
	sr, err := chat.Stream(ctx, msgs, opts...)
	if err != nil {
		return err
	}
	defer sr.Close()
	for {
		chunk, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if chunk != nil && chunk.Content != "" {
			fmt.Print(chunk.Content)
		}
	}
}

// PrintMessage prints assistant text and token usage to stderr.
func PrintMessage(msg *schema.Message) {
	if msg == nil {
		return
	}
	fmt.Println(msg.PlainText())
	if len(msg.ToolCalls) > 0 {
		fmt.Fprintln(os.Stderr, "--- tool calls ---")
		for _, tc := range msg.ToolCalls {
			fmt.Fprintf(os.Stderr, "%s(%s)\n", tc.Function.Name, tc.Function.Arguments)
		}
	}
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		u := msg.ResponseMeta.Usage
		fmt.Fprintf(os.Stderr, "tokens: prompt=%d completion=%d total=%d\n",
			u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
}

// PrintRunMetrics waits for async metrics record.
func PrintRunMetrics(store *metrics.MemoryStore, runID string) {
	if store == nil || runID == "" {
		return
	}
	deadline := time.After(3 * time.Second)
	for {
		if rec, ok := store.Get(runID); ok {
			b, _ := json.MarshalIndent(rec, "", "  ")
			fmt.Fprintf(os.Stderr, "--- run metrics ---\n%s\n", b)
			return
		}
		select {
		case <-deadline:
			fmt.Fprintln(os.Stderr, "metrics: not ready")
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// PrintSnapshot prints store aggregate metrics.
func PrintSnapshot(store *metrics.MemoryStore) {
	if store == nil {
		return
	}
	b, _ := json.MarshalIndent(store.Snapshot(), "", "  ")
	fmt.Fprintf(os.Stderr, "--- snapshot ---\n%s\n", b)
}

// Fatal exits on non-nil error.
func Fatal(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// UserMessages builds system + user messages.
func UserMessages(system, prompt string) []*schema.Message {
	prompt = strings.TrimSpace(prompt)
	if system == "" {
		return []*schema.Message{schema.UserMessage(prompt)}
	}
	return []*schema.Message{
		schema.SystemMessage(system),
		schema.UserMessage(prompt),
	}
}
