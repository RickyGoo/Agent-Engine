package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	RunDir string
}

func New(runDir string) (*Store, error) {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{RunDir: runDir}, nil
}

func (s *Store) Path(name string) string {
	return filepath.Join(s.RunDir, name)
}

func (s *Store) WriteText(name, content string) error {
	return os.WriteFile(s.Path(name), []byte(content), 0o644)
}

func (s *Store) WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteText(name, string(data))
}

func (s *Store) MustWriteJSON(name string, v any) {
	if err := s.WriteJSON(name, v); err != nil {
		panic(fmt.Sprintf("write artifact %s: %v", name, err))
	}
}
