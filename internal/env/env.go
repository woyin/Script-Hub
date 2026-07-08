package env

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/script-hub-org/script-hub/internal/httpclient"
)

// Env provides environment utilities similar to the original Env.js class.
type Env struct {
	Name     string
	http     *httpclient.Client
	store    map[string]string
	storeMu  sync.RWMutex
	timeout  int // seconds
}

// New creates a new Env instance.
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

// GetEnv returns the current environment identifier.
// In server mode, always returns "Server".
func (e *Env) GetEnv() string {
	return "Server"
}

// IsNode returns true when running in Node.js (server) mode.
func (e *Env) IsNode() bool {
	return true
}

// IsSurge returns true when running as Surge script.
func (e *Env) IsSurge() bool {
	return false
}

// IsLoon returns true when running as Loon script.
func (e *Env) IsLoon() bool {
	return false
}

// IsQuanX returns true when running as Quantumult X script.
func (e *Env) IsQuanX() bool {
	return false
}

// IsShadowrocket returns true when running as Shadowrocket script.
func (e *Env) IsShadowrocket() bool {
	return false
}

// IsStash returns true when running as Stash script.
func (e *Env) IsStash() bool {
	return false
}

// HTTPGet performs an HTTP GET request and returns the body.
func (e *Env) HTTPGet(ctx context.Context, url string) (string, error) {
	body, _, err := e.http.Get(ctx, url)
	return body, err
}

// HTTPGetWithStatus performs an HTTP GET request and returns body + status code.
func (e *Env) HTTPGetWithStatus(ctx context.Context, url string) (string, int, error) {
	return e.http.Get(ctx, url)
}

// HTTPGetWithHeaders performs an HTTP GET request with custom headers.
func (e *Env) HTTPGetWithHeaders(ctx context.Context, url string, headers map[string]string) (string, int, error) {
	return e.http.GetWithHeaders(ctx, url, headers)
}

// Getval retrieves a value from the persistent store.
func (e *Env) Getval(key string) string {
	e.storeMu.RLock()
	defer e.storeMu.RUnlock()
	return e.store[key]
}

// Setval stores a value in the persistent store.
func (e *Env) Setval(key, value string) {
	e.storeMu.Lock()
	defer e.storeMu.Unlock()
	e.store[key] = value
}

// GetJSON retrieves and parses a JSON value from the persistent store.
func (e *Env) GetJSON(key string, target interface{}) error {
	e.storeMu.RLock()
	raw, ok := e.store[key]
	e.storeMu.RUnlock()
	if !ok {
		return nil
	}
	return json.Unmarshal([]byte(raw), target)
}

// SetJSON serializes and stores a JSON value in the persistent store.
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

// ToObj parses a JSON string into the target interface.
func ToObj(s string, target interface{}) error {
	return json.Unmarshal([]byte(s), target)
}

// ToStr serializes a value to JSON string.
func ToStr(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

// Info logs an info message.
func (e *Env) Info(msg string) {
	log.Printf("[INFO][%s] %s", e.Name, msg)
}

// Warn logs a warning message.
func (e *Env) Warn(msg string) {
	log.Printf("[WARN][%s] %s", e.Name, msg)
}

// Error logs an error message.
func (e *Env) Error(msg string) {
	log.Printf("[ERROR][%s] %s", e.Name, msg)
}

// Debug logs a debug message.
func (e *Env) Debug(msg string) {
	log.Printf("[DEBUG][%s] %s", e.Name, msg)
}
