package rtmp

import (
	"bytes"
	"io"
	"testing"
)

func TestHandshake(t *testing.T) {
	// 用 io.Pipe 做真正的双向管道
	// serverRead ← clientWrite, clientRead ← serverWrite
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	// 服务端 ReadWriter
	serverRW := &pipeRW{r: serverRead, w: serverWrite}
	// 客户端 ReadWriter
	clientRW := &pipeRW{r: clientRead, w: clientWrite}

	// 构造 C0 + C1
	c0c1 := make([]byte, 1+handshakeSize)
	c0c1[0] = rtmpVersion
	for i := 1; i < len(c0c1); i++ {
		c0c1[i] = byte(i % 256)
	}

	// 启动服务端握手（异步）
	errCh := make(chan error, 1)
	go func() {
		errCh <- Handshake(serverRW)
	}()

	// 客户端发 C0+C1
	if _, err := clientRW.Write(c0c1); err != nil {
		t.Fatalf("client write C0C1: %v", err)
	}

	// 客户端读 S0+S1+S2
	s0s1s2 := make([]byte, 1+handshakeSize*2)
	if _, err := io.ReadFull(clientRW, s0s1s2); err != nil {
		t.Fatalf("client read S0S1S2: %v", err)
	}

	// 验证 S0
	if s0s1s2[0] != rtmpVersion {
		t.Fatalf("S0 = %d, want %d", s0s1s2[0], rtmpVersion)
	}

	// 客户端发 C2（echo S1）
	c2 := make([]byte, handshakeSize)
	copy(c2, s0s1s2[1:1+handshakeSize])
	if _, err := clientRW.Write(c2); err != nil {
		t.Fatalf("client write C2: %v", err)
	}

	// 等待服务端完成
	if err := <-errCh; err != nil {
		t.Fatalf("server handshake: %v", err)
	}

	// 关闭管道
	serverRead.Close()
	serverWrite.Close()
}

// pipeRW 把 reader 和 writer 组合成 ReadWriter
type pipeRW struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRW) Close() error {
	p.r.Close()
	p.w.Close()
	return nil
}

// 保留 bytes 导入用于其他测试
var _ = bytes.NewBuffer
