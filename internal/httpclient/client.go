// Input: compress/gzip, context, fmt, io, net/http, strings
// Output: type Client, func NewClient(), func (Client) Get/GetWithHeaders/Post/GetBytesWithHeaders(), func ParseCustomHeaders()
// Pos: 工具层-HTTP 客户端，封装带超时、自定义头、gzip 解压的统一请求能力
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package httpclient 提供统一的 HTTP 客户端封装。
// 支持自定义超时、自定义请求头、gzip 解压等功能，
// 对应 JS 版 Env.js 中的 HTTP 请求方法。
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

// Client 是带有可配置超时和自定义头的 HTTP 客户端。
type Client struct {
	client  *http.Client
	timeout time.Duration
	headers map[string]string // 默认请求头
}

// NewClient 创建带有指定超时（秒）的 HTTP 客户端。
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

// SetHeader 设置所有后续请求的默认请求头。
func (c *Client) SetHeader(key, value string) {
	c.headers[key] = value
}

// Get 执行带自定义头的 HTTP GET 请求。
func (c *Client) Get(ctx context.Context, url string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("创建 GET 请求失败: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	return c.doRequest(req)
}

// GetWithHeaders 执行带额外自定义头的 HTTP GET 请求。
func (c *Client) GetWithHeaders(ctx context.Context, url string, headers map[string]string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("创建 GET 请求失败: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.doRequest(req)
}

// Post 执行带自定义头的 HTTP POST 请求。
func (c *Client) Post(ctx context.Context, url string, body io.Reader) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", 0, fmt.Errorf("创建 POST 请求失败: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	return c.doRequest(req)
}

// doRequest 执行 HTTP 请求并返回字符串响应体。
func (c *Client) doRequest(req *http.Request) (string, int, error) {
	bodyBytes, status, err := c.doRequestBytes(req)
	if err != nil {
		return "", status, err
	}
	return string(bodyBytes), status, nil
}

// GetBytesWithHeaders 执行 HTTP GET 请求，返回原始字节、状态码和响应头。
func (c *Client) GetBytesWithHeaders(ctx context.Context, url string, headers map[string]string) ([]byte, int, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("创建 GET 请求失败: %w", err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("执行请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 自动解压 gzip 响应
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, nil, fmt.Errorf("创建 gzip 读取器失败: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	// 提取响应头（仅取每个头的第一个值）
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}
	return bodyBytes, resp.StatusCode, respHeaders, nil
}

// doRequestBytes 执行 HTTP 请求并返回字节响应体。
func (c *Client) doRequestBytes(req *http.Request) ([]byte, int, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("执行请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 自动解压 gzip 响应
	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, fmt.Errorf("创建 gzip 读取器失败: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	bodyBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取响应体失败: %w", err)
	}
	return bodyBytes, resp.StatusCode, nil
}

// ParseCustomHeaders 从查询参数格式的字符串解析自定义请求头。
// 格式："Key1:Value1\nKey2:Value2"，兼容 \r\n 换行符。
func ParseCustomHeaders(headerStr string) map[string]string {
	headers := make(map[string]string)
	if headerStr == "" {
		return headers
	}
	headerStr = strings.ReplaceAll(headerStr, "\r\n", "\n")
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
