package rule

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/script-hub-org/script-hub/internal/cache"
	"github.com/script-hub-org/script-hub/internal/config"
)

// 相同输入比较旧整文件转换与文件流转换。输入由服务器重复写出，
// 避免测试程序先把整个输入保存在内存。峰值是采样值，不是 RSS。
func BenchmarkRuleConversionMemory(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20} {
		for _, stream := range []bool{false, true} {
			name := "legacy"
			if stream {
				name = "stream"
			}
			b.Run(fmt.Sprintf("%s/%dMiB", name, size>>20), func(b *testing.B) {
				line := []byte("  - 'DOMAIN-SUFFIX,example.test'\n")
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, "payload:\n")
					for n := 0; n < size; n += len(line) {
						if _, err := w.Write(line); err != nil {
							return
						}
					}
				}))
				defer source.Close()
				cfg := config.LoadConfig()
				cfg.MaxBodyKB = 16 << 10
				cfg.CacheDir = b.TempDir()
				parser := NewParser(cfg)
				disk, err := cache.New(cfg.CacheDir, 86400, 0)
				if err != nil {
					b.Fatal(err)
				}
				input := ParseInput{URLs: []string{source.URL}, TargetApp: "surge-rule-set"}
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				var peak atomic.Uint64
				peak.Store(before.HeapAlloc)
				done := make(chan struct{})
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-done:
							return
						case <-ticker.C:
							var m runtime.MemStats
							runtime.ReadMemStats(&m)
							for {
								old := peak.Load()
								if m.HeapAlloc <= old || peak.CompareAndSwap(old, m.HeapAlloc) {
									break
								}
							}
						}
					}
				}()
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if stream {
						ws, err := disk.Workspace("")
						if err != nil {
							b.Fatal(err)
						}
						out, err := parser.ParseStream(context.Background(), input, ws)
						if err != nil {
							b.Fatal(err)
						}
						io.Copy(io.Discard, out.File.Reader())
						out.File.Close()
					} else {
						out, err := parser.Parse(context.Background(), input)
						if err != nil {
							b.Fatal(err)
						}
						runtime.KeepAlive(out)
					}
				}
				b.StopTimer()
				close(done)
				<-stopped
				p := peak.Load()
				if p > before.HeapAlloc {
					b.ReportMetric(float64(p-before.HeapAlloc)/(1<<20), "peak-heap-MiB")
				}
			})
		}
	}
}
