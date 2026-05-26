package cfg

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type File struct {
	Bucket  string `json:"bucket"`
	Profile string `json:"profile,omitempty"`
	Layout  string `json:"layout,omitempty"`
}

const (
	LayoutFlat   = "flat"
	LayoutNested = "nested"
)

func (f *File) EffectiveLayout() string {
	if f.Layout == LayoutNested {
		return LayoutNested
	}
	return LayoutFlat
}

func path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "sigrets.json"), nil
}

func Load() (*File, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &File{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func Save(f *File) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func Path() string {
	p, _ := path()
	return p
}
