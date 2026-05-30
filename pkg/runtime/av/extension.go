package av

import (
	"context"

	pmedi "github.com/LingByte/LingVoice/pkg/protocol/media"
)

// Extension adds optional behavior on top of the kernel without forking SessionRuntime.
type Extension interface {
	Name() string
	Capabilities() []pmedi.Capability
	Attach(k *Kernel) error
	OnEvent(ctx context.Context, ev pmedi.SessionEvent) error
}

// ExtensionFunc adapts functions to Extension.
type ExtensionFunc struct {
	Nm   string
	Caps []pmedi.Capability
	OnAttach func(k *Kernel) error
	OnEv     func(ctx context.Context, ev pmedi.SessionEvent) error
}

func (e ExtensionFunc) Name() string { return e.Nm }
func (e ExtensionFunc) Capabilities() []pmedi.Capability { return e.Caps }
func (e ExtensionFunc) Attach(k *Kernel) error {
	if e.OnAttach != nil {
		return e.OnAttach(k)
	}
	return nil
}
func (e ExtensionFunc) OnEvent(ctx context.Context, ev pmedi.SessionEvent) error {
	if e.OnEv != nil {
		return e.OnEv(ctx, ev)
	}
	return nil
}

// HandoffExtension suppresses cognitive turns while human handoff is active.
type HandoffExtension struct {
	active bool
}

func NewHandoffExtension() *HandoffExtension { return &HandoffExtension{} }

func (h *HandoffExtension) Name() string { return "handoff" }
func (h *HandoffExtension) Capabilities() []pmedi.Capability {
	return []pmedi.Capability{pmedi.CapHandoff}
}
func (h *HandoffExtension) Attach(k *Kernel) error { return nil }

func (h *HandoffExtension) OnEvent(_ context.Context, ev pmedi.SessionEvent) error {
	if v := handoffFromMeta(ev); v != nil {
		h.active = *v
	}
	return nil
}

func (h *HandoffExtension) Active() bool { return h.active }

func handoffFromMeta(ev pmedi.SessionEvent) *bool {
	if ev.Meta == nil {
		return nil
	}
	if v, ok := ev.Meta["handoff_active"].(bool); ok {
		return &v
	}
	return nil
}
