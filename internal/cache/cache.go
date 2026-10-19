// Input: os, encoding/json, encoding/binary, crypto/sha256, sync, time, internal/types
// Output: Cache, New, Get, Set, Cleanup, Workspace
// Pos: 工具层-有容量上限的流式磁盘结果缓存与转换临时空间
package cache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/script-hub-org/script-hub/internal/types"
)

const DefaultMaxBytes int64 = 800 << 20
const magic = "SHCACHE2"
const maxMetadata = 64 << 10

// ErrCapacity 表示实际文件总量将超过配置的磁盘预算。
var ErrCapacity = errors.New("insufficient cache capacity")
var ErrUnavailable = errors.New("disk cache unavailable")

type metadata struct {
	ExpiresAt time.Time
	Status    int
	Headers   map[string]string
	Size      int64
}
type record struct {
	size    int64
	expires time.Time
	created time.Time
	pins    int
	retired bool
}

// Cache 只在内存保留文件索引及容量计数，不保存正文。
// 一个目录由一个服务进程管理；预算包含临时空间和正在读取的旧文件。
type Cache struct {
	mu       sync.Mutex
	dir      string
	ttl      time.Duration
	maxBytes int64
	used     int64
	files    map[string]*record
}

func New(dir string, ttlSeconds int, maxBytes int64) (*Cache, error) {
	if ttlSeconds <= 0 {
		return nil, nil
	}
	if dir == "" {
		return nil, fmt.Errorf("cache directory is empty")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	probe, err := os.CreateTemp(dir, ".probe-")
	if err != nil {
		return nil, err
	}
	probe.Close()
	os.Remove(probe.Name())
	c := &Cache{dir: dir, ttl: time.Duration(ttlSeconds) * time.Second, maxBytes: maxBytes, files: make(map[string]*record)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if strings.HasPrefix(e.Name(), ".work-") || strings.HasPrefix(e.Name(), ".tmp-") || strings.HasPrefix(e.Name(), ".probe-") || strings.HasSuffix(e.Name(), ".json") {
			if err := os.RemoveAll(path); err != nil {
				return nil, err
			}
			continue
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bin") {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		m, _, err := readMetadata(f)
		info, statErr := f.Stat()
		f.Close()
		if err != nil || statErr != nil || time.Now().After(m.ExpiresAt) {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			continue
		}
		c.files[path] = &record{size: info.Size(), expires: m.ExpiresAt, created: info.ModTime()}
		c.used += info.Size()
	}
	if err := c.evict(0, ""); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:])+".bin")
}

func readMetadata(f *os.File) (metadata, int64, error) {
	var m metadata
	var prefix [12]byte
	if _, err := io.ReadFull(f, prefix[:]); err != nil {
		return m, 0, err
	}
	if string(prefix[:8]) != magic {
		return m, 0, fmt.Errorf("invalid cache format")
	}
	n := binary.BigEndian.Uint32(prefix[8:])
	if n == 0 || n > maxMetadata {
		return m, 0, fmt.Errorf("invalid metadata length")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return m, 0, err
	}
	if err := json.Unmarshal(buf, &m); err != nil {
		return m, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		return m, 0, err
	}
	offset := int64(12 + n)
	if m.Status != 200 || m.Size < 0 || m.Size != info.Size()-offset {
		return m, 0, fmt.Errorf("invalid cache body size")
	}
	return m, offset, nil
}

func (c *Cache) Get(key string) (types.ResponseData, bool) {
	if c == nil {
		return types.ResponseData{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	path := c.path(key)
	rec := c.files[path]
	if rec != nil && time.Now().After(rec.expires) {
		c.remove(path)
		return types.ResponseData{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return types.ResponseData{}, false
	}
	m, offset, err := readMetadata(f)
	if err != nil || time.Now().After(m.ExpiresAt) {
		f.Close()
		c.remove(path)
		return types.ResponseData{}, false
	}
	if rec == nil {
		info, _ := f.Stat()
		rec = &record{size: info.Size(), expires: m.ExpiresAt, created: info.ModTime()}
		c.files[path] = rec
		c.used += rec.size
	}
	rec.pins++
	body := types.NewFileBody(f, offset, m.Size, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		rec.pins--
		if rec.retired && rec.pins == 0 {
			c.used -= rec.size
		}
	})
	return types.ResponseData{Status: m.Status, Headers: m.Headers, File: body}, true
}

func (c *Cache) Set(key string, rd types.ResponseData) error {
	if c == nil {
		return nil
	}
	if rd.Incomplete || rd.Status != 200 {
		return fmt.Errorf("response is not cacheable")
	}
	size := int64(len(rd.Body))
	var source io.Reader = strings.NewReader(rd.Body)
	if rd.File != nil {
		size = rd.File.Size()
		source = rd.File.Reader()
	}
	data, err := json.Marshal(metadata{ExpiresAt: time.Now().Add(c.ttl), Status: rd.Status, Headers: rd.Headers, Size: size})
	if err != nil {
		return err
	}
	if len(data) > maxMetadata {
		return fmt.Errorf("metadata exceeds limit")
	}
	workspace, err := c.Workspace(key)
	if err != nil {
		return err
	}
	defer workspace.Close()
	f, err := workspace.Create()
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := make([]byte, 12)
	copy(prefix, magic)
	binary.BigEndian.PutUint32(prefix[8:], uint32(len(data)))
	if _, err = f.Write(prefix); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if n, err := io.CopyBuffer(f, source, make([]byte, 64<<10)); err != nil {
		return err
	} else if n != size {
		return fmt.Errorf("incomplete cache write")
	}
	if err := f.Close(); err != nil {
		return err
	}
	// 将已写入临时文件的实际额度转交正式文件，不重复计费。
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	path := c.path(key)
	if err := os.Rename(f.File.Name(), path); err != nil {
		return err
	}
	if old := c.files[path]; old != nil {
		old.retired = true
		if old.pins == 0 {
			c.used -= old.size
		}
	}
	c.files[path] = &record{size: f.size, expires: time.Now().Add(c.ttl), created: time.Now()}
	workspace.used -= f.size
	return nil
}

func (c *Cache) remove(path string) error {
	rec := c.files[path]
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(c.files, path)
	if rec != nil {
		rec.retired = true
		if rec.pins == 0 {
			c.used -= rec.size
		}
	}
	return nil
}

// evict 在锁内执行；读取者持有的文件及待刷新的原结果不会被容量淘汰。
func (c *Cache) evict(reserve int64, protect string) error {
	if reserve > c.maxBytes {
		return ErrCapacity
	}
	now := time.Now()
	type candidate struct {
		path string
		rec  *record
	}
	var live []candidate
	for path, rec := range c.files {
		if path == protect || rec.pins > 0 {
			continue
		}
		if now.After(rec.expires) {
			if err := c.remove(path); err != nil {
				return err
			}
		} else {
			live = append(live, candidate{path, rec})
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].rec.created.Before(live[j].rec.created) })
	for _, item := range live {
		if c.used+reserve <= c.maxBytes {
			break
		}
		if err := c.remove(item.path); err != nil {
			return err
		}
	}
	if c.used+reserve > c.maxBytes {
		return ErrCapacity
	}
	return nil
}
func (c *Cache) Cleanup() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evict(0, "")
}

// Workspace 按临时文件的实际长度计入全局预算，不预留固定额度。
// 输出被响应持有时，其文件额度一直保留到工作目录清理。
type Workspace struct {
	mu      sync.Mutex
	dir     string
	protect string
	used    int64
	cache   *Cache
	closed  bool
	once    sync.Once
}
type TempFile struct {
	File      *os.File
	workspace *Workspace
	size      int64
}

func (c *Cache) Workspace(key string) (*Workspace, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	dir, err := os.MkdirTemp(c.dir, ".work-")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return &Workspace{dir: dir, protect: c.path(key), cache: c}, nil
}
func (w *Workspace) Create() (*TempFile, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, os.ErrClosed
	}
	f, err := os.CreateTemp(w.dir, "part-")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return &TempFile{File: f, workspace: w}, nil
}
func (f *TempFile) Write(p []byte) (int, error) {
	w := f.workspace
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	offset, err := f.File.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	growth := max(int64(0), offset+int64(len(p))-f.size)
	c := w.cache
	c.mu.Lock()
	if growth > c.maxBytes-c.used {
		err = c.evict(growth, w.protect)
	}
	if err == nil {
		c.used += growth
	}
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	n, err := f.File.Write(p)
	if err != nil {
		err = fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	actual := max(int64(0), offset+int64(n)-f.size)
	if n == 0 {
		actual = 0
	}
	f.size += actual
	w.used += actual
	// 短写或失败只计入已经写到磁盘的字节。
	c.mu.Lock()
	c.used -= growth - actual
	c.mu.Unlock()
	return n, err
}
func (f *TempFile) Reset() error {
	w := f.workspace
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if err := f.File.Truncate(0); err != nil {
		return err
	}
	w.cache.mu.Lock()
	w.cache.used -= f.size
	w.cache.mu.Unlock()
	w.used -= f.size
	f.size = 0
	_, err := f.File.Seek(0, io.SeekStart)
	return err
}
func (w *Workspace) Close() {
	w.once.Do(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.closed = true
		if err := os.RemoveAll(w.dir); err != nil {
			// 清理失败不能释放仍占用磁盘的额度；重启时会再次清理。
			log.Printf("转换临时文件清理失败: %v", err)
			return
		}
		w.cache.mu.Lock()
		w.cache.used -= w.used
		w.cache.mu.Unlock()
		w.used = 0
	})
}

// 防止 os.File.ReadFrom 绕过临时空间计数。
func (f *TempFile) ReadFrom(r io.Reader) (int64, error) {
	return io.CopyBuffer(struct{ io.Writer }{f}, r, make([]byte, 64<<10))
}

func (f *TempFile) Size() int64 { return f.size }

func (f *TempFile) Read(p []byte) (int, error)                   { return f.File.Read(p) }
func (f *TempFile) Seek(offset int64, whence int) (int64, error) { return f.File.Seek(offset, whence) }
func (f *TempFile) Close() error                                 { return f.File.Close() }
func (f *TempFile) Stat() (os.FileInfo, error)                   { return f.File.Stat() }

// CreatedAt 仅查看有效缓存索引，不读取或持有正文。
func (c *Cache) CreatedAt(key string) (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec := c.files[c.path(key)]
	if rec == nil || time.Now().After(rec.expires) {
		return time.Time{}, false
	}
	return rec.created, true
}
