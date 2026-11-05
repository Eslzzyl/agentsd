package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultBaseURL  = "https://api.openai.com/v1"
	maxResponseBody = 32 << 20
	maxErrorBody    = 1 << 20
)

type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("openai: base URL must be an absolute http or https URL without a query or fragment")
	}
	baseURL = strings.TrimRight(parsed.String(), "/")

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

func (c *Client) endpoint() string {
	return c.baseURL + "/chat/completions"
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.httpClient.Do(req)
}

type APIError struct {
	StatusCode int
	Type       string
	Code       string
	Param      string
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("openai: API returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("openai: API returned HTTP %d", e.StatusCode)
}

type apiErrorBody struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Param   string `json:"param"`
		Message string `json:"message"`
	} `json:"error"`
}

func readAPIError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("openai: read API error response: %w", err)
	}

	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("x-request-id"),
	}
	var decoded apiErrorBody
	if json.Unmarshal(body, &decoded) == nil {
		apiErr.Type = decoded.Error.Type
		apiErr.Code = decoded.Error.Code
		apiErr.Param = decoded.Error.Param
		apiErr.Message = decoded.Error.Message
	}
	if apiErr.Message == "" && len(body) > 0 {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	return apiErr
}

func decodeResponse(body io.Reader, dst any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBody+1))
	if err != nil {
		return fmt.Errorf("openai: read response: %w", err)
	}
	if len(data) > maxResponseBody {
		return fmt.Errorf("openai: response exceeds %d bytes", maxResponseBody)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("openai: decode response: %w", err)
	}
	return nil
}

func marshalRequest(value any) (*bytes.Reader, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}
	return bytes.NewReader(data), nil
}

func checkHTTPStatus(resp *http.Response) error {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	return readAPIError(resp)
}
