package av

import (
	"context"
	"io"
	"sync"

	medi "github.com/LingByte/LingVoice/pkg/media"
)

// ChanTransport is an in-memory MediaTransport for local demos and tests.
type ChanTransport struct {
	name   string
	dir    string
	session *medi.MediaSession
	in     chan medi.MediaPacket
	out    chan medi.MediaPacket
	once   sync.Once
}

// NewChanTransport builds a transport with buffered channels.
func NewChanTransport(name, direction string, buf int) *ChanTransport {
	if buf <= 0 {
		buf = 32
	}
	return &ChanTransport{
		name: name,
		dir:  direction,
		in:   make(chan medi.MediaPacket, buf),
		out:  make(chan medi.MediaPacket, buf),
	}
}

func (t *ChanTransport) String() string { return "ChanTransport(" + t.name + "/" + t.dir + ")" }

func (t *ChanTransport) Attach(s *medi.MediaSession) { t.session = s }

func (t *ChanTransport) Codec() medi.CodecConfig { return medi.DefaultCodecConfig() }

func (t *ChanTransport) Next(ctx context.Context) (medi.MediaPacket, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case p, ok := <-t.in:
		if !ok {
			return nil, io.EOF
		}
		return p, nil
	}
}

func (t *ChanTransport) Send(_ context.Context, packet medi.MediaPacket) (int, error) {
	if packet == nil {
		return 0, nil
	}
	n := len(packet.Body())
	select {
	case t.out <- packet:
	default:
	}
	return n, nil
}

func (t *ChanTransport) Close() error {
	t.once.Do(func() { close(t.in) })
	return nil
}

// Inject pushes a packet into the transport read side (simulates peer input).
func (t *ChanTransport) Inject(p medi.MediaPacket) {
	select {
	case t.in <- p:
	default:
	}
}

// Out returns the channel receiving packets sent by the session.
func (t *ChanTransport) Out() <-chan medi.MediaPacket { return t.out }
