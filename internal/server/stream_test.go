package server

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

type chunkReader struct {
	s string
	n int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.s) == 0 {
		return 0, io.EOF
	}
	n := r.n
	if n > len(r.s) {
		n = len(r.s)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.s[:n])
	r.s = r.s[n:]
	return n, nil
}
func TestReplaceStreamBoundaries(t *testing.T) {
	input := strings.Repeat("a", 65530) + "https://script.hub/one\nhttp://script.hub/two\nhttps://script.hub/three"
	for _, chunk := range []int{1, 2, 7, 17, 18, 19, 32768, 65536} {
		var out strings.Builder
		err := replaceStream(&out, &chunkReader{s: input, n: chunk}, "https://public.example")
		want := strings.ReplaceAll(strings.ReplaceAll(input, "https://script.hub/", "https://public.example/"), "http://script.hub/", "https://public.example/")
		if err != nil || out.String() != want {
			t.Fatalf("chunk %d: %v", chunk, err)
		}
	}
}

func BenchmarkReplaceStream(b *testing.B) {
	for _, size := range []int{1 << 20, 10 << 20, 50 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			input := strings.Repeat("a", size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := replaceStream(io.Discard, strings.NewReader(input), "https://public.example"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
