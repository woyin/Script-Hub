// Input: compress/gzip, context, fmt, io, net/http, strings
// Output: type Client, func NewClient(), func (Client) Get/GetWithHeaders(), func (Client) applyHeaders/bodyReader/readLimited/doRequest/doRequestBytes(), func ParseCustomHeaders()
// Deps: internal/ssrf（启用时注入受控 Transport）
// Pos: 工具层-HTTP 客户端，封装带超时、自定义头、gzip 解压、响应体上限的统一请求能力
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package httpclient 提供统一的 HTTP 客户端封装。
// 支持自定义超时、自定义请求头、gzip 解压、响应体大小上限等功能，
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

	"github.com/script-hub-org/script-hub/internal/ssrf"
)

// 默认响应体上限（KB），仅在 NewClient 未显式指定时使用。
const defaultMaxBodyKB = 600

// Client 是带有可配置超时和自定义头的 HTTP 客户端。
type Client struct {
	client    *http.Client
	timeout   time.Duration
	maxBodyKB int               // 响应体最大字节数（KB），超过则中止读取并报错
	headers   map[string]string // 默认请求头
}

// NewClient 创建带有指定超时（秒）的 HTTP 客户端。
// maxBodyKB 限制单次响应体最大字节数（按 KB 计），<=0 时使用默认值。
//
// 当 ssrf.Enabled=true 时，使用内置 SSRF 校验的 Transport：
// DNS 解析、IP 校验与实际 TCP 连接在 DialContext 阶段原子完成，
// 消除"先检查再连接"的 DNS rebinding TOCTOU 窗口。
func NewClient(timeoutSec int, maxBodyKB int) *Client {
	if maxBodyKB <= 0 {
		maxBodyKB = defaultMaxBodyKB
	}
	hc := &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
	}
	// SSRF 启用时注入受控 Transport；校验发生在拨号阶段。
	if ssrf.Enabled {
		hc.Transport = ssrf.NewTransport()
	}
	return &Client{
		client:    hc,
		timeout:   time.Duration(timeoutSec) * time.Second,
		maxBodyKB: maxBodyKB,
		headers: map[string]string{
			"User-Agent": "script-hub/1.0.0",
		},
	}
}

// Get 执行带自定义头的 HTTP GET 请求。
func (c *Client) Get(ctx context.Context, url string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("创建 GET 请求失败: %w", err)
	}
	c.applyHeaders(req, nil)
	return c.doRequest(req)
}

// GetWithHeaders 执行带额外自定义头的 HTTP GET 请求。
func (c *Client) GetWithHeaders(ctx context.Context, url string, headers map[string]string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("创建 GET 请求失败: %w", err)
	}
	c.applyHeaders(req, headers)
	return c.doRequest(req)
}

// applyHeaders 将默认头与本次额外头合并写入请求；额外头同名时覆盖默认头。
func (c *Client) applyHeaders(req *http.Request, extra map[string]string) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
}

// doRequest 执行 HTTP 请求并返回字符串响应体。
func (c *Client) doRequest(req *http.Request) (string, int, error) {
	bodyBytes, status, err := c.doRequestBytes(req)
	if err != nil {
		return "", status, err
	}
	return string(bodyBytes), status, nil
}

// doRequestBytes 执行 HTTP 请求并返回字节响应体。
func (c *Client) doRequestBytes(req *http.Request) ([]byte, int, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("执行请求失败: %w", err)
	}
	defer resp.Body.Close()

	reader, cleanup, err := c.bodyReader(resp)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	bodyBytes, err := c.readLimited(reader)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return bodyBytes, resp.StatusCode, nil
}

// bodyReader 根据 Content-Encoding 选择合适的 reader。
// 返回的 cleanup（非 nil 时）用于关闭 gzip.Reader 等需要显式释放的资源。
func (c *Client) bodyReader(resp *http.Response) (io.Reader, func(), error) {
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("创建 gzip 读取器失败: %w", err)
		}
		return gzReader, func() { gzReader.Close() }, nil
	}
	return resp.Body, nil, nil
}

// readLimited 读取 reader 内容，但最多读取 maxBodyKB*1024 字节。
// 超过上限时返回错误，避免上游恶意大响应导致 OOM。
func (c *Client) readLimited(reader io.Reader) ([]byte, error) {
	limit := int64(c.maxBodyKB) * 1024
	// 多读 1 字节用于判断是否越限
	lr := io.LimitReader(reader, limit+1)
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("响应体超过上限 %dKB", c.maxBodyKB)
	}
	return buf, nil
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
