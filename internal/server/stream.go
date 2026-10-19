// Input: io, bytes, net/http, internal/types
// Output: sendResponse, replaceStream
// Pos: API层-固定缓冲区文件发送及跨块地址替换
package server

import (
	"bytes"
	"io"
	"log"
	"net/http"

	"github.com/script-hub-org/script-hub/internal/types"
)

func sendResponse(w http.ResponseWriter, rd types.ResponseData, base string, replace bool) {
	for k, v := range rd.Headers {
		w.Header().Set(k, v)
	}
	if rd.File == nil {
		if replace {
			writeBody(w, rd.Status, nil, rd.Body, base)
		} else {
			w.WriteHeader(rd.Status)
			io.WriteString(w, rd.Body)
		}
		return
	}
	defer rd.File.Close()
	w.WriteHeader(rd.Status)
	var err error
	if replace {
		err = replaceStream(w, rd.File.Reader(), base)
	} else {
		_, err = io.CopyBuffer(w, rd.File.Reader(), make([]byte, 64<<10))
	}
	if err != nil {
		log.Printf("文件响应发送中断: %v", err)
	}
}

// replaceStream 保留最长匹配串的尾部，支持跨读取边界的地址替换。
func replaceStream(dst io.Writer, src io.Reader, base string) error {
	patterns := [][]byte{[]byte("https://script.hub/"), []byte("http://script.hub/")}
	replacement := []byte(base + "/")
	buffer := make([]byte, 64<<10)
	storage := make([]byte, len(buffer)+len(patterns[0]))
	pending := storage[:0]
	drain := func(final bool) error {
		keep := len(patterns[0]) - 1
		if final {
			keep = 0
		}
		for len(pending) > keep {
			safe := len(pending) - keep
			pos, which := -1, 0
			for i, pattern := range patterns {
				idx := bytes.Index(pending, pattern)
				if idx >= 0 && (pos < 0 || idx < pos) {
					pos, which = idx, i
				}
			}
			if pos >= 0 && pos < safe {
				if err := writeAll(dst, pending[:pos]); err != nil {
					return err
				}
				if err := writeAll(dst, replacement); err != nil {
					return err
				}
				pending = pending[pos+len(patterns[which]):]
			} else {
				if err := writeAll(dst, pending[:safe]); err != nil {
					return err
				}
				pending = pending[safe:]
				break
			}
		}
		return nil
	}
	for {
		n, err := src.Read(buffer)
		pending = append(pending, buffer[:n]...)
		if e := drain(err == io.EOF); e != nil {
			return e
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// 将尾部移回固定缓冲区，避免每个块重新分配正文空间。
		copy(storage, pending)
		pending = storage[:len(pending)]
	}
}
func writeAll(w io.Writer, p []byte) error {
	n, err := w.Write(p)
	if err == nil && n != len(p) {
		return io.ErrShortWrite
	}
	return err
}
