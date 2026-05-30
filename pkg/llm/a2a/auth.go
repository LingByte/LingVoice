package a2a

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// AuthConfig configures client authentication.
type AuthConfig struct {
	BearerToken string
	APIKey      string
	APIKeyHeader string // default X-API-Key
}

// ServerAuth configures handler authentication requirements.
type ServerAuth struct {
	BearerTokens []string
	APIKeys      []string
	APIKeyHeader string
	RequireMTLS  bool
}

// Authenticator validates incoming requests.
type Authenticator interface {
	Authenticate(r *http.Request) error
}

type multiAuth struct {
	server ServerAuth
}

func (a multiAuth) Authenticate(r *http.Request) error {
	if a.server.RequireMTLS {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return fmt.Errorf("a2a: mTLS client certificate required")
		}
	}
	header := a.server.APIKeyHeader
	if header == "" {
		header = "X-API-Key"
	}
	if key := r.Header.Get(header); key != "" && len(a.server.APIKeys) > 0 {
		for _, allowed := range a.server.APIKeys {
			if subtle.ConstantTimeCompare([]byte(key), []byte(allowed)) == 1 {
				return nil
			}
		}
		return fmt.Errorf("a2a: invalid api key")
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") && len(a.server.BearerTokens) > 0 {
		token := strings.TrimPrefix(auth, "Bearer ")
		for _, allowed := range a.server.BearerTokens {
			if subtle.ConstantTimeCompare([]byte(token), []byte(allowed)) == 1 {
				return nil
			}
		}
		return fmt.Errorf("a2a: invalid bearer token")
	}
	if len(a.server.BearerTokens) > 0 || len(a.server.APIKeys) > 0 {
		return fmt.Errorf("a2a: authentication required")
	}
	return nil
}

// AuthMiddleware wraps handlers with authentication.
func AuthMiddleware(auth ServerAuth, next http.Handler) http.Handler {
	if len(auth.BearerTokens) == 0 && len(auth.APIKeys) == 0 && !auth.RequireMTLS {
		return next
	}
	validator := multiAuth{server: auth}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/agent-card.json" || r.URL.Path == "/.well-known/agent.json" || r.URL.Path == "/agent.json" {
			next.ServeHTTP(w, r)
			return
		}
		if err := validator.Authenticate(r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ApplyAuthHeaders sets client auth headers on a request.
func ApplyAuthHeaders(req *http.Request, cfg AuthConfig) {
	if req == nil {
		return
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}
	if cfg.APIKey != "" {
		header := cfg.APIKeyHeader
		if header == "" {
			header = "X-API-Key"
		}
		req.Header.Set(header, cfg.APIKey)
	}
}

// DefaultAuthSchemes returns auth schemes for AgentCard from server config.
func DefaultAuthSchemes(auth ServerAuth) []AuthScheme {
	var schemes []AuthScheme
	for range auth.BearerTokens {
		schemes = append(schemes, AuthScheme{Type: "bearer", Description: "Bearer token in Authorization header"})
		break
	}
	if len(auth.APIKeys) > 0 {
		header := auth.APIKeyHeader
		if header == "" {
			header = "X-API-Key"
		}
		schemes = append(schemes, AuthScheme{Type: "api_key", Header: header})
	}
	if auth.RequireMTLS {
		schemes = append(schemes, AuthScheme{Type: "mtls", Description: "Mutual TLS client certificate"})
	}
	return schemes
}

// NoopAuthenticator allows all requests.
type NoopAuthenticator struct{}

func (NoopAuthenticator) Authenticate(_ *http.Request) error { return nil }

// ContextWithTaskID stores a task id in context.
type contextKey string

const taskIDKey contextKey = "a2a_task_id"

func ContextWithTaskID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, taskIDKey, id)
}

func TaskIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(taskIDKey).(string)
	return v
}
