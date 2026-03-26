package githubclient

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// FileCache is a small disk-backed cache for file contents keyed by a stable cache key.
// It exists to make conditional requests (ETag/If-None-Match) usable: when GitHub returns
// 304 Not Modified, we can still return the previously fetched content.
type FileCache struct {
	mu  sync.Mutex
	dir string
}

func NewFileCache(dir string) *FileCache {
	_ = os.MkdirAll(dir, 0o755)
	return &FileCache{dir: dir}
}

func (c *FileCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.pathForKey(key))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (c *FileCache) Set(key string, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pathForKey(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func (c *FileCache) pathForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(c.dir, h[:2], h)
}
