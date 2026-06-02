// Package llm defines provider-agnostic chat model interfaces built on
// [github.com/LingByte/LingVoice/pkg/protocol/schema] messages.
//
// Implementations (OpenAI, Ollama, …) live outside this package; protocol only
// specifies contracts and streaming helpers.
package llm
