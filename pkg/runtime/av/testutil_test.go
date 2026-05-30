package av_test

import (
	"context"
	"testing"

	"github.com/LingByte/LingVoice/pkg/media"
)

func newTestMediaSession(t testing.TB) *media.MediaSession {
	t.Helper()
	return media.NewDefaultSession().Context(context.Background())
}
