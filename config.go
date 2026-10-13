package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const configName = "slicetrigger.json"

type Config struct {
	RadioIP     string `json:"radio_ip"`
	Station     string `json:"station"`
	AutoConnect bool   `json:"auto_connect"`
	Port        int    `json:"port"`
	Actions     []Act  `json:"actions"`
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".slicetrigger*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// configPath returns slicetrigger.json next to the exe when that directory
// is writable, otherwise %APPDATA%\SliceTrigger\slicetrigger.json.
func configPath() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if dirWritable(dir) {
			return filepath.Join(dir, configName)
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "SliceTrigger", configName)
}

func LoadConfig(path string) (*Config, error) {
	c := &Config{Port: 8765}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return &Config{Port: 8765}, err
	}
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = 8765
	}
	return c, nil
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
