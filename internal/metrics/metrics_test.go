package metrics

import (
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
