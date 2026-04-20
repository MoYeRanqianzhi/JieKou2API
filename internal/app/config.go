package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig  `yaml:"server"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Auth     AuthConfig    `yaml:"auth"`
	Limits   LimitsConfig  `yaml:"limits"`
}

type ServerConfig struct {
	ListenAddr string   `yaml:"listen"`
	APIKeys    []string `yaml:"api_keys"`
}

type UpstreamConfig struct {
	BaseURL      string `yaml:"base_url"`
	DefaultModel string `yaml:"default_model"`
}

type AuthConfig struct {
	Dir           string        `yaml:"dir"`
	WatchInterval time.Duration `yaml:"watch_interval"`
	Breaker       BreakerConfig `yaml:"breaker"`
}

type BreakerConfig struct {
	Threshold int           `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

type LimitsConfig struct {
	GlobalRPM  int `yaml:"global_rpm"`
	AccountRPM int `yaml:"account_rpm"`
	ClientRPM  int `yaml:"client_rpm"`
}

const DefaultConfigPath = "config.yaml"

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Server.ListenAddr == "" {
		c.Server.ListenAddr = ":8080"
	}
	if c.Upstream.BaseURL == "" {
		c.Upstream.BaseURL = "https://jiekou.ai"
	}
	c.Upstream.BaseURL = strings.TrimRight(c.Upstream.BaseURL, "/")
	if c.Upstream.DefaultModel == "" {
		c.Upstream.DefaultModel = "claude-opus-4-7"
	}
	if c.Auth.Dir == "" {
		c.Auth.Dir = "auths"
	}
	if c.Auth.WatchInterval <= 0 {
		c.Auth.WatchInterval = 15 * time.Second
	}
	if c.Auth.Breaker.Threshold <= 0 {
		c.Auth.Breaker.Threshold = 3
	}
	if c.Auth.Breaker.Cooldown <= 0 {
		c.Auth.Breaker.Cooldown = 1 * time.Hour
	}
	c.Server.APIKeys = dedupStrings(c.Server.APIKeys)
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, k := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

func fingerprint(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:6] + "…" + key[len(key)-2:]
}
