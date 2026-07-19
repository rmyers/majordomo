package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// Config holds the application configuration and manages its own persistence.
type Config struct {
	dir    string
	Server ServerConfig `json:"server"`
	LLM    LLMConfig    `json:"llm"`
}

// LLMConfig holds settings for the local LLM server.
type LLMConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	URL      string `json:"url"`
	APIKey   string `json:"apiKey,omitempty"`
}

// ServerConfig holds settings for the api server.
type ServerConfig struct {
	Host string `json:"host"`
	Port string `json:"port"`
}

// New creates a Config that reads/writes to the given directory.
// If dir is empty, uses the default user config directory (~/.config/majordomo).
func New(dir string) *Config {
	if dir == "" {
		dir = defaultDir()
	}
	config := loadConfig(dir)
	return &config
}

// Default returns a Config with no settings, using the default directory.
func Default() *Config {
	return New("")
}

// Dir returns the directory this config reads from and writes to.
func (c *Config) Dir() string {
	return c.dir
}

// Path returns the full path to the config file.
func (c *Config) Path() string {
	return configFile(c.dir)
}

// Load reads the config file from disk and merges it on top of defaults.
func (c *Config) Load() error {
	data, err := os.ReadFile(c.Path())
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return err
	}
	return nil
}

// Save writes the config to disk.
func (c *Config) Save() error {
	os.MkdirAll(c.dir, 0o755)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path(), data, 0o644)
}

// GetModel returns the configured model name.
func (c *Config) GetModel() string {
	return c.LLM.Model
}

// SetModel sets the model name.
func (c *Config) SetModel(model string) {
	c.LLM.Model = model
}

// GetURL returns the configured server URL.
func (c *Config) GetURL() string {
	return c.LLM.URL
}

// SetURL sets the server URL.
func (c *Config) SetURL(url string) {
	c.LLM.URL = url
}

// GetAPIKey returns the configured API key.
func (c *Config) GetAPIKey() string {
	return c.LLM.APIKey
}

// SetAPIKey sets the API key.
func (c *Config) SetAPIKey(key string) {
	c.LLM.APIKey = key
}

// GetSessionsDir returns the directory where session files are stored.
func (c *Config) GetSessionsDir() (string, error) {
	return filepath.Join(c.dir, "sessions"), nil
}

// defaultDir returns the default config directory.
func defaultDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "majordomo")
}

// configFile returns the path to the config file in the config directory
func configFile(dir string) string {
	return filepath.Join(dir, "config.json")
}

// load reads the config file from disk and merges it on top of defaults.
func loadConfig(dir string) Config {
	defaults := Config{
		dir:    dir,
		Server: ServerConfig{Host: "localhost", Port: "3636"},
		LLM:    LLMConfig{Provider: "local", URL: "http://localhost:11434", Model: "llama3.2"},
	}
	data, err := os.ReadFile(configFile(dir))
	if err != nil {
		slog.Warn("Error reading config file", slog.Any("error", err))
		return defaults
	}

	// Read in the data and replace the defaults
	if err := json.Unmarshal(data, &defaults); err != nil {
		slog.Warn("JSON parse error", slog.Any("error", err))
		return defaults
	}
	return defaults
}
