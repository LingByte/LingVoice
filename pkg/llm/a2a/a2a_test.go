package a2a_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/llm/a2a"
	"github.com/LingByte/LingVoice/pkg/protocol/schema"
)

func TestA2AServerClientWithAuth(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + ln.Addr().String()
	token := "secret"
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "test",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Streaming: true, Tasks: true},
		},
		Auth: a2a.ServerAuth{BearerTokens: []string{token}},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("ok", nil), nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

	client := a2a.NewClient(a2a.ClientConfig{Auth: a2a.AuthConfig{BearerToken: token}})
	msg, err := client.SendMessages(context.Background(), baseURL, []*schema.Message{schema.UserMessage("hi")})
	if err != nil || msg.Content != "ok" {
		t.Fatalf("msg err=%v content=%q", err, msg.Content)
	}

	bad := a2a.NewClient(a2a.ClientConfig{Auth: a2a.AuthConfig{BearerToken: "wrong"}})
	if _, err := bad.SendMessages(context.Background(), baseURL, []*schema.Message{schema.UserMessage("hi")}); err == nil {
		t.Fatal("expected unauthorized")
	}
}

func TestA2AStreaming(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + ln.Addr().String()
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "stream",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
			Capabilities:       a2a.AgentCapabilities{Streaming: true},
		},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("sync", nil), nil
		},
		StreamHandler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](1)
			go func() {
				defer sw.Close()
				sw.Send(schema.AssistantMessage("chunk", nil), nil)
			}()
			return sr, nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

	client := a2a.NewClient()
	sr, err := client.StreamMessages(context.Background(), baseURL, []*schema.Message{schema.UserMessage("x")})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := schema.CollectMessages(sr)
	if err != nil || msg.Content != "chunk" {
		t.Fatalf("stream err=%v msg=%q", err, msg.Content)
	}
}

func TestA2MTLS(t *testing.T) {
	dir := t.TempDir()
	caFile, certFile, keyFile := genTestCerts(t, dir)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{Name: "mtls"},
		Auth: a2a.ServerAuth{RequireMTLS: true},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("mtls-ok", nil), nil
		},
	})
	srv, err := a2a.NewMTLSServer(ln.Addr().String(), h.HTTPHandler(), a2a.TLSConfig{
		CertFile: certFile, KeyFile: keyFile, CAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = a2a.ServeMTLS(srv, ln) }()
	defer shutdown(srv)

	client, err := a2a.NewMTLSHTTPClient(a2a.TLSConfig{
		CertFile: certFile, KeyFile: keyFile, CAFile: caFile, InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "https://" + ln.Addr().String()
	a2aClient := a2a.NewClient(a2a.ClientConfig{HTTPClient: client})
	msg, err := a2aClient.SendMessages(context.Background(), baseURL, []*schema.Message{schema.UserMessage("x")})
	if err != nil || msg.Content != "mtls-ok" {
		t.Fatalf("err=%v msg=%q", err, msg.Content)
	}
}

func shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func genTestCerts(t *testing.T, dir string) (caFile, certFile, keyFile string) {
	t.Helper()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test CA"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caFile = filepath.Join(dir, "ca.pem")
	writePEM(caFile, "CERTIFICATE", caDER)

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, caTmpl, &key.PublicKey, caKey)
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	writePEM(certFile, "CERTIFICATE", certDER)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	writePEM(keyFile, "PRIVATE KEY", keyDER)
	return caFile, certFile, keyFile
}

func writePEM(path, typ string, der []byte) {
	f, _ := os.Create(path)
	defer f.Close()
	_ = pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func TestRemoteAgent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + ln.Addr().String()
	h := a2a.NewHandler(a2a.HandlerConfig{
		Card: a2a.AgentCard{
			Name:               "remote",
			URL:                "/",
			PreferredTransport: a2a.TransportJSONRPC,
		},
		Handler: func(_ context.Context, _ *a2a.MessageRequest) (*schema.Message, error) {
			return schema.AssistantMessage("remote-ok", nil), nil
		},
	})
	srv := &http.Server{Handler: h.HTTPHandler()}
	go func() { _ = srv.Serve(ln) }()
	defer shutdown(srv)

	ra := &a2a.RemoteAgent{Client: a2a.NewClient(), BaseURL: baseURL}
	msg, err := ra.Generate(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err != nil || msg.Content != "remote-ok" {
		t.Fatalf("err=%v msg=%q", err, msg.Content)
	}
}

var _ = tls.VersionTLS12
