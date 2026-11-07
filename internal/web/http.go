package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMaxResponseBytes = 4 << 20
	defaultMaxErrorBytes    = 16 << 10
)

func (runtime *Runtime) do(ctx context.Context, provider, method, target string, headers http.Header, body []byte, maxBytes int64) ([]byte, http.Header, error) {
	parsed, err := validateURL(target)
	if err != nil {
		return nil, nil, providerError(provider, 400, err.Error(), nil)
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	requestBody := bytes.NewReader(body)
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), requestBody)
	if err != nil {
		return nil, nil, providerError(provider, 0, "create request", err)
	}
	request.Header.Set("User-Agent", runtime.UserAgent)
	request.Header.Set("Accept", "application/json, text/plain, text/html, text/event-stream;q=0.9")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response, err := runtime.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, providerError(provider, 0, "request failed", err)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if readErr != nil {
		return nil, response.Header, providerError(provider, response.StatusCode, "read response", readErr)
	}
	if int64(len(data)) > maxBytes {
		return nil, response.Header, providerError(provider, response.StatusCode, fmt.Sprintf("response exceeds %d bytes", maxBytes), nil)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if len(message) > defaultMaxErrorBytes {
			message = message[:defaultMaxErrorBytes]
		}
		if message == "" {
			message = response.Status
		}
		return nil, response.Header, providerError(provider, response.StatusCode, message, nil)
	}
	return data, response.Header, nil
}

func (runtime *Runtime) getText(ctx context.Context, provider, target string, headers http.Header, maxBytes int64) (string, http.Header, error) {
	data, responseHeaders, err := runtime.do(ctx, provider, http.MethodGet, target, headers, nil, maxBytes)
	return string(data), responseHeaders, err
}

func (runtime *Runtime) postJSON(ctx context.Context, provider, target string, headers http.Header, payload any, maxBytes int64) ([]byte, http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, providerError(provider, 0, "encode request", err)
	}
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", "application/json")
	return runtime.do(ctx, provider, http.MethodPost, target, headers, body, maxBytes)
}

func decodeJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func validateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("URL must be an absolute http or https URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme %q is not supported", parsed.Scheme)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("URL userinfo is not supported")
	}
	return parsed, nil
}

func withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func parseMCPResponse(data []byte) (map[string]any, error) {
	var direct map[string]any
	if json.Unmarshal(data, &direct) == nil {
		return direct, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		if json.Unmarshal([]byte(line), &direct) == nil {
			return direct, nil
		}
	}
	return nil, fmt.Errorf("MCP response is not valid JSON")
}

func mapValue(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func intValue(value any) int {
	switch value := value.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}
