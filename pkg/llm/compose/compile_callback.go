package compose

import "context"

// CompileCallback is invoked after a graph compiles successfully (Eino compile hook subset).
type CompileCallback func(ctx context.Context, info GraphInfo) error

// WithCompileCallback registers a post-compile hook.
func WithCompileCallback(fn CompileCallback) GraphCompileOption {
	return func(c *graphCompileConfig) {
		c.compileCallback = fn
	}
}
