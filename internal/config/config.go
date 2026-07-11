package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds the application configuration.
type Config struct {
	LLM LLMConfig `json:"llm"`
}

// LLMConfig holds settings for the local LLM server.
type LLMConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	URL      string `json:"url"`
	APIKey   string `json:"apiKey,omitempty"`
}

// Default returns a Config with auto-detection enabled for local LLM servers.
func Default() *Config {
	return &Config{
		LLM: LLMConfig{Provider: "auto", Model: "", URL: ""},
	}
}

// Load reads a config file from the given path.
// If path is empty, uses the default config directory.
func Load(path string) (*Config, error) {
	if path == "" {
		path = configPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the config to disk.
func Save(cfg *Config) error {
	path := configPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "majordomo", "config.json")
}
