package a2a

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
)

// TLSConfig configures mTLS for A2A clients and servers.
type TLSConfig struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	ServerName string
	InsecureSkipVerify bool
}

// NewMTLSHTTPClient builds an HTTP client with optional client cert and CA pool.
func NewMTLSHTTPClient(cfg TLSConfig) (*http.Client, error) {
	tlsCfg, err := buildTLSConfig(cfg, true)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

// NewMTLSServer builds an http.Server with TLS settings.
func NewMTLSServer(addr string, handler http.Handler, cfg TLSConfig) (*http.Server, error) {
	tlsCfg, err := buildTLSConfig(cfg, false)
	if err != nil {
		return nil, err
	}
	if cfg.RequireClientCert() {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsCfg,
	}, nil
}

func (c TLSConfig) RequireClientCert() bool {
	return c.CAFile != "" && c.CertFile != "" && c.KeyFile != ""
}

func buildTLSConfig(cfg TLSConfig, client bool) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
	}
	if cfg.CAFile != "" {
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("a2a tls: read ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("a2a tls: invalid ca pem")
		}
		if client {
			tlsCfg.RootCAs = pool
		} else {
			tlsCfg.ClientCAs = pool
		}
	}
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("a2a tls: load cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

// ServeMTLS serves on an existing listener with TLS.
func ServeMTLS(srv *http.Server, ln net.Listener) error {
	if srv == nil || ln == nil {
		return fmt.Errorf("a2a: nil server or listener")
	}
	if srv.TLSConfig != nil {
		return srv.Serve(tls.NewListener(ln, srv.TLSConfig))
	}
	return srv.Serve(ln)
}
