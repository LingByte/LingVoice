package llm

import "errors"

// ErrNotImplemented indicates the ChatModel does not support the requested method.
var ErrNotImplemented = errors.New("llm: not implemented")
