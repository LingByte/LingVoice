package httputil

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DoJSON sends a JSON request and decodes a JSON response into out.
func DoJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newHTTPError(resp.StatusCode, string(raw))
	}
	if out == nil {
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// HTTPError is a non-2xx API response.
type HTTPError struct {
	StatusCode int
	Body       string
	Code       string // provider error code when parsed from JSON body
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "http error"
	}
	if e.Code != "" {
		return fmt.Sprintf("http %d (%s): %s", e.StatusCode, e.Code, truncate(e.Body, 512))
	}
	return fmt.Sprintf("http %d: %s", e.StatusCode, truncate(e.Body, 512))
}

func newHTTPError(status int, body string) *HTTPError {
	return NewHTTPError(status, body)
}

// NewHTTPError builds an HTTPError with parsed provider code (for tests and adapters).
func NewHTTPError(status int, body string) *HTTPError {
	return &HTTPError{
		StatusCode: status,
		Body:       body,
		Code:       parseAPIErrorCode(body),
	}
}

func parseAPIErrorCode(body string) string {
	var wrap struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		return ""
	}
	if wrap.Error.Code != "" {
		return wrap.Error.Code
	}
	if wrap.Error.Type != "" {
		return wrap.Error.Type
	}
	return wrap.Type
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// SSEHandler handles one Server-Sent Events data line payload (without "data: " prefix).
type SSEHandler func(data string) error

// ReadSSE reads an SSE stream until EOF, invoking handle for each data payload.
func ReadSSE(r io.Reader, handle SSEHandler) error {
	sc := bufio.NewScanner(r)
	// Allow large JSON chunks from providers.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if err := handle(data); err != nil {
			return err
		}
	}
	return sc.Err()
}

// PostSSE posts JSON and reads an SSE response body.
func PostSSE(ctx context.Context, client *http.Client, url string, headers map[string]string, body any, handle SSEHandler) error {
	if client == nil {
		client = http.DefaultClient
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return newHTTPError(resp.StatusCode, string(raw))
	}
	return ReadSSE(resp.Body, handle)
}
