// Package config holds prview's folders, port and access key.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPort is registered in ~/.ports.csv.
const DefaultPort = 7450

// CacheDir is where repos, worktrees, reading orders and chats are kept.
func CacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "prview")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "prview")
}

// ConfigDir holds the access key.
func ConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "prview")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "prview")
}

// Key returns the secret that must be in the link the first time a browser
// opens prview. It's created on first use.
func Key() (string, error) {
	path := filepath.Join(ConfigDir(), "key")
	if b, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(b)); len(k) >= 32 {
			return k, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	k := hex.EncodeToString(buf)
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		return "", err
	}
	return k, os.WriteFile(path, []byte(k+"\n"), 0o600)
}
