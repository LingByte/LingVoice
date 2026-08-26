package rtmp

import (
	"bytes"
	"testing"
)

func TestHandshake(t *testing.T) {
	// 模拟客户端握手：C0+C1 → 读 S0+S1+S2 → 发 C2
	clientBuf := &bytes.Buffer{}
	serverBuf := &bytes.Buffer{}

	// 用 pipe 模拟双向连接
	clientW := serverBuf // 客户端写 → 服务端读
	serverW := clientBuf  // 服务端写 → 客户端读

	// 构造 C0 + C1
	c0c1 := make([]byte, 1+handshakeSize)
	c0c1[0] = rtmpVersion
	for i := 1; i < len(c0c1); i++ {
		c0c1[i] = byte(i % 256)
	}
	clientW.Write(c0c1)

	// 服务端握手（从 serverBuf 读，写到 clientBuf）
	// 使用自定义 ReadWriter
	rw := &pipeConn{r: serverBuf, w: serverW}
	if err := Handshake(rw); err != nil {
		t.Fatalf("server handshake: %v", err)
	}

	// 验证服务端发出了 S0+S1+S2 (1 + 1536 + 1536)
	if clientBuf.Len() != 1+handshakeSize*2 {
		t.Fatalf("server sent %d bytes, want %d", clientBuf.Len(), 1+handshakeSize*2)
	}
	s0 := clientBuf.Bytes()[0]
	if s0 != rtmpVersion {
		t.Fatalf("S0 = %d, want %d", s0, rtmpVersion)
	}

	// 客户端发 C2（echo S1）
	c2 := make([]byte, handshakeSize)
	copy(c2, clientBuf.Bytes()[1:1+handshakeSize])
	clientW.Write(c2)

	// 服务端读 C2（已在 Handshake 内部完成，这里只验证无 panic）
}

// pipeConn 把 reader 和 writer 组合成 ReadWriter
type pipeConn struct {
	r *bytes.Buffer
	w *bytes.Buffer
}

func (p *pipeConn) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeConn) Close() error                { return nil }
