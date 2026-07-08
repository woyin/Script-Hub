package httpclient

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps http.Client with configurable timeout and custom headers.
type Client struct {
	client  *http.Client
	timeout time.Duration
	headers map[string]string
}

// NewClient creates a new HTTP client with the given timeout in seconds.
func NewClient(timeoutSec int) *Client {
	return &Client{
		client: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
		timeout: time.Duration(timeoutSec) * time.Second,
		headers: map[string]string{
			"User-Agent": "script-hub/1.0.0",
		},
	}
}

// SetHeader sets a custom header for all subsequent requests.
func (c *Client) SetHeader(key, value string) {
	c.headers[key] = value
}

// Get performs an HTTP GET request with custom headers.
func (c *Client) Get(ctx context.Context, url string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("creating GET request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	return c.doRequest(req)
}

// GetWithHeaders performs an HTTP GET request with additional custom headers.
func (c *Client) GetWithHeaders(ctx context.Context, url string, headers map[string]string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("creating GET request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.doRequest(req)
}

// Post performs an HTTP POST request with custom headers.
func (c *Client) Post(ctx context.Context, url string, body io.Reader) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", 0, fmt.Errorf("creating POST request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	return c.doRequest(req)
}

func (c *Client) doRequest(req *http.Request) (string, int, error) {
	bodyBytes, status, err := c.doRequestBytes(req)
	if err != nil {
		return "", status, err
	}
	return string(bodyBytes), status, nil
}

// GetBytesWithHeaders performs an HTTP GET and returns raw bytes, status, and response headers.
func (c *Client) GetBytesWithHeaders(ctx context.Context, url string, headers map[string]string) ([]byte, int, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("creating GET request: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, nil, fmt.Errorf("creating gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, nil, fmt.Errorf("reading response body: %w", err)
	}

	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}
	return bodyBytes, resp.StatusCode, respHeaders, nil
}

func (c *Client) doRequestBytes(req *http.Request) ([]byte, int, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, fmt.Errorf("creating gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading response body: %w", err)
	}
	return bodyBytes, resp.StatusCode, nil
}

// ParseCustomHeaders parses custom headers from the format used in query parameters.
// Format: "Key1:Value1\nKey2:Value2"
func ParseCustomHeaders(headerStr string) map[string]string {
	headers := make(map[string]string)
	if headerStr == "" {
		return headers
	}
	lines := strings.Split(headerStr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, ":")
		if idx > 0 && idx < len(line)-1 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			if key != "" && value != "" {
				headers[key] = value
			}
		}
	}
	return headers
}
