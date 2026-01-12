package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DefaultRegion  string       `yaml:"default_region"`
	DefaultService string       `yaml:"default_service"`
	Server         ServerConfig `yaml:"server"`
	Cache          CacheConfig  `yaml:"cache"`
	MaxConcurrency int          `yaml:"max_concurrency"`
	Regions        []string     `yaml:"regions"`
}

type ServerConfig struct {
	Port string `yaml:"port"`
}

type CacheConfig struct {
	TTLMinutes int `yaml:"ttl_minutes"`
}

// Default configuration
func Default() *Config {
	return &Config{
		DefaultRegion:  "us-east-1",
		DefaultService: "ec2",
		Server: ServerConfig{
			Port: "8080",
		},
		Cache: CacheConfig{
			TTLMinutes: 5,
		},
		MaxConcurrency: 10,
		Regions:        []string{},
	}
}

// Load configuration from file
func Load(filename string) (*Config, error) {
	// Start with defaults
	cfg := Default()

	// If file doesn't exist, return defaults
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return cfg, nil
	}

	// Read file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	// Parse YAML
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// GetCacheTTL returns the cache TTL as a duration
func (c *Config) GetCacheTTL() time.Duration {
	return time.Duration(c.Cache.TTLMinutes) * time.Minute
}

// GetPort returns the server port, checking environment variable first
func (c *Config) GetPort() string {
	if port := os.Getenv("PORT"); port != "" {
		return port
	}
	return c.Server.Port
}
