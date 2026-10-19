// Input: io, os, sync
// Output: FileBody, NewFileBody, Reader, Retain, Close
// Pos: 数据层-文件响应的独立读取位置及引用生命周期
package types

import (
	"io"
	"os"
	"sync"
)

// FileBody 是不可变文件正文。每个读取者使用独立的 SectionReader。
// refs 保护 singleflight 等待方及仍在发送的响应。
type FileBody struct {
	mu      sync.Mutex
	file    *os.File
	offset  int64
	size    int64
	refs    int
	cleanup func()
}

func NewFileBody(file *os.File, offset, size int64, cleanup func()) *FileBody {
	return &FileBody{file: file, offset: offset, size: size, refs: 1, cleanup: cleanup}
}
func (b *FileBody) Reader() io.Reader { return io.NewSectionReader(b.file, b.offset, b.size) }
func (b *FileBody) Size() int64       { return b.size }
func (b *FileBody) Retain(n int)      { b.mu.Lock(); b.refs += n; b.mu.Unlock() }
func (b *FileBody) Close() error {
	b.mu.Lock()
	b.refs--
	last := b.refs == 0
	b.mu.Unlock()
	if !last {
		return nil
	}
	err := b.file.Close()
	if b.cleanup != nil {
		b.cleanup()
	}
	return err
}
