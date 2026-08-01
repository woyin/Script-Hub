package metrics

import (
	"fmt"
	"strings"
	"testing"
)

func TestMetrics_Render(t *testing.T) {
	m := New()
	m.IncRequest()
	m.IncRequest()
	m.IncConversion("qx-rewrite", "surge-module")
	m.IncConversion("qx-rewrite", "surge-module")
	m.IncConversion("rule-set", "surge-rule-set")
	m.IncConversionError()
	m.IncFetchError()
	m.IncCacheHit()
	m.IncCacheMiss()
	m.IncCacheMiss()

	var b strings.Builder
	m.Render(&b)
	out := b.String()

	checks := []string{
		"scripthub_requests_total 2",
		"scripthub_conversion_errors_total 1",
		"scripthub_fetch_errors_total 1",
		"scripthub_cache_hits_total 1",
		"scripthub_cache_misses_total 2",
		`scripthub_conversions_total{source="qx-rewrite",target="surge-module"} 2`,
		`scripthub_conversions_total{source="rule-set",target="surge-rule-set"} 1`,
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("missing %q in output:\n%s", c, out)
		}
	}
}

func TestMetrics_ConcurrentSafe(t *testing.T) {
	m := New()
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			m.IncRequest()
			m.IncConversion("a", "b")
			m.IncCacheHit()
			var b strings.Builder
			m.Render(&b)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}

// TestIncConversion_LabelCardinalityCapped 验证 conversionsByKey 有基数上限：
// 超过 maxLabelKeys 后，新的 source×target 组合归并为 "unknown|unknown"，
// 防止恶意客户端传任意 target 导致 label 集合无限增长。
func TestIncConversion_LabelCardinalityCapped(t *testing.T) {
	m := New()
	// 填满 maxLabelKeys 个不同组合
	for i := 0; i < maxLabelKeys; i++ {
		m.IncConversion("qx-rewrite", fmt.Sprintf("target-%d", i))
	}
	before := m.labelCount()
	if before != maxLabelKeys {
		t.Fatalf("after filling: labelCount = %d, want %d", before, maxLabelKeys)
	}
	// 超限后新组合应归并到 unknown|unknown，不再新增非 unknown key
	m.IncConversion("qx-rewrite", "new-attacker-target")
	m.IncConversion("another-source", "another-target")
	after := m.labelCount()
	// 上限是 maxLabelKeys 个正常 key + 1 个 unknown 溢出桶
	if after > maxLabelKeys+1 {
		t.Errorf("after overflow: labelCount = %d, want <= %d (new keys should merge into unknown)", after, maxLabelKeys+1)
	}
	if after != maxLabelKeys+1 {
		t.Errorf("expected unknown overflow bucket to be created, got labelCount = %d", after)
	}
	// unknown|unknown 应被递增
	var b strings.Builder
	m.Render(&b)
	if !strings.Contains(b.String(), `source="unknown",target="unknown"`) {
		t.Errorf("overflowed conversions should merge to unknown|unknown:\n%s", b.String())
	}
}

// labelCount 返回 conversionsByKey 当前 key 数（测试专用）。
func (m *Metrics) labelCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conversionsByKey)
}
