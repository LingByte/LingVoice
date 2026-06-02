// Package schema defines core LLM message types shared across LingVoice.
//
// Design follows the same role/content/tool-call model used by OpenAI Chat
// Completions and frameworks like CloudWeGo Eino, adapted for our stack.
//
// Key types:
//
//   - [Message] — unit passed between users, models, and tools
//   - [ToolCall], [ToolInfo] — function calling
//   - [ResponseMeta], [TokenUsage] — model response metadata
//   - [ConcatMessages] — merge streaming chunks into one message
package schema
