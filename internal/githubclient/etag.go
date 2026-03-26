package githubclient

import (
	"encoding/json"
	"os"
	"sync"
)

type ETagStore struct {
	mu   sync.Mutex
	Tags map[string]string `json:"tags"`
}

func NewETagStore() *ETagStore {
	return &ETagStore{Tags: map[string]string{}}
}

func LoadETagStore(path string) (*ETagStore, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return NewETagStore(), err
	}
	var s ETagStore
	if err := json.Unmarshal(b, &s); err != nil {
		return NewETagStore(), err
	}
	if s.Tags == nil {
		s.Tags = map[string]string{}
	}
	return &s, nil
}

func (s *ETagStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.Tags[key]
	return t, ok
}

func (s *ETagStore) Set(key, etag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tags[key] = etag
}

func (s *ETagStore) Save(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
