// Input: fmt, sync/atomic
// Output: type Metrics, func New(), func (Metrics) Inc*(), func (Metrics) Render()
// Pos: 工具层-运行时指标收集与 Prometheus 文本格式输出
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package metrics 提供轻量级运行时指标收集，输出 Prometheus 文本格式。
// 不依赖 prometheus client 库，使用原子计数器实现，零外部依赖。
//
// 计数范围：所有指标只统计【转换请求】（rewrite/rule 解析路径），不包含
// 基础设施端点 /healthz、/version、/、/formats、/metrics 自身。这是有意的——
// /healthz 常被探活高频调用，计入会严重稀释业务转换 QPS 的信号；
// /metrics 自指更不应计入。若需总 HTTP 请求量，请在反向代理层统计。
package metrics

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// Metrics 收集 Script Hub 运行时的核心指标。
// 所有计数器使用原子操作，可被多个 goroutine 并发更新。
//
// 注意：requestsTotal 只统计转换请求，不含 /healthz、/metrics 等基础设施端点
// （见包文档说明）。
type Metrics struct {
	// 基础计数
	requestsTotal    atomic.Int64 // 所有转换请求（不含静态端点）
	conversionErrors atomic.Int64 // 转换失败次数（parser 返回错误）
	fetchErrors      atomic.Int64 // 上游 HTTP fetch 失败次数
	cacheHits        atomic.Int64 // 缓存命中次数
	cacheMisses      atomic.Int64 // 缓存未命中次数

	// 按 source×target 维度的转换计数
	mu               sync.Mutex
	conversionsByKey map[string]int64
}

// New 创建一个空的 Metrics 收集器。
func New() *Metrics {
	return &Metrics{
		conversionsByKey: make(map[string]int64),
	}
}

// IncRequest 递增总请求数。
func (m *Metrics) IncRequest() { m.requestsTotal.Add(1) }

// IncConversionError 递增转换失败数。
func (m *Metrics) IncConversionError() { m.conversionErrors.Add(1) }

// IncFetchError 递增上游 fetch 失败数。
func (m *Metrics) IncFetchError() { m.fetchErrors.Add(1) }

// IncCacheHit 递增缓存命中数。
func (m *Metrics) IncCacheHit() { m.cacheHits.Add(1) }

// IncCacheMiss 递增缓存未命中数。
func (m *Metrics) IncCacheMiss() { m.cacheMisses.Add(1) }

// IncConversion 按 source 和 target 维度递增一次转换计数。
// source 或 target 为空时用 "unknown" 占位，保证 label 稳定。
//
// 计数口径：每次转换请求都会调一次 IncConversion（含缓存命中路径），
// 因此 conversions_total 可大于 requests_total——命中时 requests_total 只 +1
// 而 conversions_total 也 +1（同一请求），正常情况下两者应相等；
// 但命中路径会在 miss 路径之外额外计数，故阅读指标时勿假设
// conversions_total <= requests_total。
//
// 约束：source 与 target 不得含 "|"（用于拼接 key，见 splitKey）。
// 当前所有 config 常量均不含此字符。
func (m *Metrics) IncConversion(source, target string) {
	if source == "" {
		source = "unknown"
	}
	if target == "" {
		target = "unknown"
	}
	m.mu.Lock()
	m.conversionsByKey[source+"|"+target]++
	m.mu.Unlock()
}

// Render 将指标写入 w，格式为 Prometheus exposition text (text/plain; version=0.0.4)。
func (m *Metrics) Render(w io.Writer) {
	reqTotal := m.requestsTotal.Load()
	cerr := m.conversionErrors.Load()
	ferr := m.fetchErrors.Load()
	hits := m.cacheHits.Load()
	misses := m.cacheMisses.Load()

	fmt.Fprintf(w, "# HELP scripthub_requests_total Total conversion requests served.\n")
	fmt.Fprintf(w, "# TYPE scripthub_requests_total counter\n")
	fmt.Fprintf(w, "scripthub_requests_total %d\n\n", reqTotal)

	fmt.Fprintf(w, "# HELP scripthub_conversion_errors_total Total failed conversions.\n")
	fmt.Fprintf(w, "# TYPE scripthub_conversion_errors_total counter\n")
	fmt.Fprintf(w, "scripthub_conversion_errors_total %d\n\n", cerr)

	fmt.Fprintf(w, "# HELP scripthub_fetch_errors_total Total upstream fetch failures.\n")
	fmt.Fprintf(w, "# TYPE scripthub_fetch_errors_total counter\n")
	fmt.Fprintf(w, "scripthub_fetch_errors_total %d\n\n", ferr)

	fmt.Fprintf(w, "# HELP scripthub_cache_hits_total Cache hits.\n")
	fmt.Fprintf(w, "# TYPE scripthub_cache_hits_total counter\n")
	fmt.Fprintf(w, "scripthub_cache_hits_total %d\n\n", hits)

	fmt.Fprintf(w, "# HELP scripthub_cache_misses_total Cache misses.\n")
	fmt.Fprintf(w, "# TYPE scripthub_cache_misses_total counter\n")
	fmt.Fprintf(w, "scripthub_cache_misses_total %d\n\n", misses)

	fmt.Fprintf(w, "# HELP scripthub_conversions_total Conversions by source and target.\n")
	fmt.Fprintf(w, "# TYPE scripthub_conversions_total counter\n")

	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.conversionsByKey))
	for k := range m.conversionsByKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		source, target := splitKey(k)
		fmt.Fprintf(w, "scripthub_conversions_total{source=%q,target=%q} %d\n", source, target, m.conversionsByKey[k])
	}
}

// splitKey 将 "source|target" 拆回两个值。
func splitKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}
