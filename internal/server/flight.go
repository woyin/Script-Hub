// flight.go 实现一个最小化的 singleflight：对相同 key 的并发调用，
// 只有首个调用执行给定的函数，其余调用阻塞并复用其结果。
//
// 用于防止缓存击穿（cache stampede）：缓存过期瞬间，N 个并发请求不会
// 同时穿透到上游，只有 1 个会真正执行 Parse，其余等待后直接读缓存。
//
// 不引入 golang.org/x/sync/singleflight，保持项目零外部依赖。
package server

import "sync"

// call 代表一次进行中的单飞调用。
type call struct {
	wg  sync.WaitGroup
	val any // 函数返回值（成功时）
	err error
}

// singleflight 合并相同 key 的并发调用。
type singleflight struct {
	mu     sync.Mutex
	inflight map[string]*call
}

func newSingleflight() *singleflight {
	return &singleflight{inflight: make(map[string]*call)}
}

// do 对 key 执行 fn。若已有相同 key 的调用在进行，则阻塞等待其结果。
// 返回 (结果, 是否为本调用的真实执行)。
func (g *singleflight) do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if c, ok := g.inflight[key]; ok {
		g.mu.Unlock()
		c.wg.Wait() // 复用进行中的调用
		return c.val, c.err
	}
	c := &call{}
	c.wg.Add(1)
	g.inflight[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.inflight, key)
	g.mu.Unlock()
	return c.val, c.err
}
