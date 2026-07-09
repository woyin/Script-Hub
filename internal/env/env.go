// Package env 提供运行时环境抽象层，对应 JS 版的 Env 工具类。
// 在服务端模式下，所有平台检测（IsSurge/IsLoon 等）返回 false，
// GetEnv() 固定返回 "Server"，与 JS 版 Node.js 环境下的行为一致。
package env

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/script-hub-org/script-hub/internal/httpclient"
)

// Env 提供环境检测、HTTP 请求、持久化存储等工具方法。
// 对应 JS 版 script-hub.js / Rewrite-Parser.js 中的 Env 类。
type Env struct {
	Name    string            // 脚本/模块名称
	http    *httpclient.Client
	store   map[string]string // 简易键值存储（替代 $persistentStore）
	storeMu sync.RWMutex
	timeout int               // HTTP 请求超时（秒）
}

// New 创建一个新的 Env 实例。timeoutSec 默认为 20 秒。
func New(name string, timeoutSec int) *Env {
	if timeoutSec <= 0 {
		timeoutSec = 20
	}
	return &Env{
		Name:    name,
		http:    httpclient.NewClient(timeoutSec),
		store:   make(map[string]string),
		timeout: timeoutSec,
	}
}

// GetEnv 返回当前运行环境标识。服务端模式下固定返回 "Server"。
func (e *Env) GetEnv() string {
	return "Server"
}

// IsNode 在服务端模式下始终返回 true。
func (e *Env) IsNode() bool {
	return true
}

// IsSurge 在服务端模式下始终返回 false。
func (e *Env) IsSurge() bool {
	return false
}

// IsLoon 在服务端模式下始终返回 false。
func (e *Env) IsLoon() bool {
	return false
}

// IsQuanX 在服务端模式下始终返回 false。
func (e *Env) IsQuanX() bool {
	return false
}

// IsShadowrocket 在服务端模式下始终返回 false。
func (e *Env) IsShadowrocket() bool {
	return false
}

// IsStash 在服务端模式下始终返回 false。
func (e *Env) IsStash() bool {
	return false
}

// HTTPGet 执行 HTTP GET 请求，返回响应体。
func (e *Env) HTTPGet(ctx context.Context, url string) (string, error) {
	body, _, err := e.http.Get(ctx, url)
	return body, err
}

// HTTPGetWithStatus 执行 HTTP GET 请求，返回响应体和状态码。
func (e *Env) HTTPGetWithStatus(ctx context.Context, url string) (string, int, error) {
	return e.http.Get(ctx, url)
}

// HTTPGetWithHeaders 执行带自定义头的 HTTP GET 请求。
func (e *Env) HTTPGetWithHeaders(ctx context.Context, url string, headers map[string]string) (string, int, error) {
	return e.http.GetWithHeaders(ctx, url, headers)
}

// Getval 从持久化存储中读取值（对应 JS 版的 $.getval / $persistentStore.read）。
func (e *Env) Getval(key string) string {
	e.storeMu.RLock()
	defer e.storeMu.RUnlock()
	return e.store[key]
}

// Setval 向持久化存储中写入值（对应 JS 版的 $.setval / $persistentStore.write）。
func (e *Env) Setval(key, value string) {
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	e.store[key] = value
}

// GetJSON 从持久化存储中读取并解析 JSON 值。
func (e *Env) GetJSON(key string, target interface{}) error {
	e.storeMu.RLock()
	raw, ok := e.store[key]
	e.storeMu.RUnlock()
	if !ok {
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}

// SetJSON 将值序列化为 JSON 并存入持久化存储。
func (e *Env) SetJSON(key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	e.store[key] = string(data)
	return nil
}

// ToObj 将 JSON 字符串解析为对象。
func ToObj(s string, target interface{}) error {
	return json.Unmarshal([]byte(s), target)
}

// ToStr 将对象序列化为 JSON 字符串。
func ToStr(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

// Info 记录信息级别日志。
func (e *Env) Info(msg string) {
	log.Printf("[INFO][%s] %s", e.Name, msg)
}

// Warn 记录警告级别日志。
func (e *Env) Warn(msg string) {
	log.Printf("[WARN][%s] %s", e.Name, msg)
}

// Error 记录错误级别日志。
func (e *Env) Error(msg string) {
	log.Printf("[ERROR][%s] %s", e.Name, msg)
}

// Debug 记录调试级别日志。
func (e *Env) Debug(msg string) {
	log.Printf("[DEBUG][%s] %s", e.Name, msg)
}
