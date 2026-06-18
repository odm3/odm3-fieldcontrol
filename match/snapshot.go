package match

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Storer persists a failsafe snapshot so a mid-match reboot comes up in Fault.
type Storer interface {
	Save(State) error
	Load() (State, bool, error)
	Clear() error
}

// FileStorer persists failsafe state to a JSON file.
type FileStorer struct {
	Path string
}

func (f *FileStorer) Save(s State) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Write atomically via a temp file + rename.
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func (f *FileStorer) Load() (State, bool, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, err
	}
	return s, true, nil
}

func (f *FileStorer) Clear() error {
	err := os.Remove(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// MemStorer is an in-memory Storer for testing.
type MemStorer struct {
	data *State
}

func (m *MemStorer) Save(s State) error {
	cp := s
	m.data = &cp
	return nil
}

func (m *MemStorer) Load() (State, bool, error) {
	if m.data == nil {
		return State{}, false, nil
	}
	return *m.data, true, nil
}

func (m *MemStorer) Clear() error {
	m.data = nil
	return nil
}
