// Input: crypto/sha256, sync, time, net/http
// Output: refreshLimiter, allowPublicRefresh
// Pos: API层-公开强制刷新的同键冷却及进程总频率限制
package server

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type refreshLimiter struct {
	mu           sync.Mutex
	cooldown     time.Duration
	maxPerMinute int
	keys         map[[32]byte]time.Time
	attempts     []time.Time
	now          func() time.Time
}

func newRefreshLimiter(cooldown, maximum int) *refreshLimiter {
	if cooldown <= 0 {
		cooldown = 60
	}
	if cooldown > 86400 {
		cooldown = 86400
	}
	if maximum > 1000 {
		maximum = 1000
	}
	if maximum <= 0 {
		maximum = 10
	}
	return &refreshLimiter{cooldown: time.Duration(cooldown) * time.Second, maxPerMinute: maximum, keys: make(map[[32]byte]time.Time), now: time.Now}
}
func (l *refreshLimiter) allow(key string) time.Duration {
	if l == nil || key == "" {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	hash := sha256.Sum256([]byte(key))
	for k, until := range l.keys {
		if !now.Before(until) {
			delete(l.keys, k)
		}
	}
	if until, ok := l.keys[hash]; ok {
		return until.Sub(now)
	}
	first := 0
	for first < len(l.attempts) && !l.attempts[first].After(now.Add(-time.Minute)) {
		first++
	}
	l.attempts = append(l.attempts[:0], l.attempts[first:]...)
	if len(l.attempts) >= l.maxPerMinute {
		return l.attempts[0].Add(time.Minute).Sub(now)
	}
	// 即使全局配置很大，也限制同键状态表的内存。
	if len(l.keys) >= 4096 {
		return time.Minute
	}
	l.keys[hash] = now.Add(l.cooldown)
	l.attempts = append(l.attempts, now)
	return 0
}
func (s *Server) allowPublicRefresh(w http.ResponseWriter, key string) bool {
	wait := s.refreshLimit.allow(key)
	if wait <= 0 {
		return true
	}
	seconds := int64((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", fmt.Sprint(seconds))
	writeAPIError(w, http.StatusTooManyRequests, "refresh rate limit exceeded")
	return false
}
