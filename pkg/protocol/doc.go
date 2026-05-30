// Package protocol defines shared data structures and interfaces for LingVoice
// orchestration. It is the foundation layer (L0): provider-agnostic message
// schema, streaming primitives, and ChatModel contracts.
//
// Layout:
//
//   - schema — messages, tools, multimodal parts, stream merge helpers
//   - llm    — ChatModel interfaces and call options
//
// Higher layers (orchestrate, voice) must depend on protocol, not the reverse.
package protocol
