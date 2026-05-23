package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr    string `yaml:"listen_addr"`
	IAMURL        string `yaml:"iam_url"`
	DS3GatewayURL string `yaml:"ds3_gateway_url"`

	Auth      AuthConfig      `yaml:"auth"`
	Query     QueryConfig     `yaml:"query"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

type AuthConfig struct {
	TokenExpiry        time.Duration `yaml:"token_expiry"`
	RefreshTokenExpiry time.Duration `yaml:"refresh_token_expiry"`
}

type QueryConfig struct {
	MaxRows          int    `yaml:"max_rows"`
	MaxExecutionSecs int    `yaml:"max_execution_seconds"`
	MaxResultBytes   int64  `yaml:"max_result_bytes"`
	PoolSize         int    `yaml:"pool_size"`
	Threads          int    `yaml:"threads"`
	MemoryLimit      string `yaml:"memory_limit"`
}

type RateLimitConfig struct {
	QueriesPerMinute int `yaml:"queries_per_minute"`
}

func Default() *Config {
	return &Config{
		ListenAddr:    ":8080",
		IAMURL:         "https://api.eu00wi.cubbit.services",
		DS3GatewayURL: "http://localhost:9000",
		Auth: AuthConfig{
			TokenExpiry:        24 * time.Hour,
			RefreshTokenExpiry: 720 * time.Hour,
		},
		Query: QueryConfig{
			MaxRows:          10000,
			MaxExecutionSecs: 60,
			MaxResultBytes:   104857600,
			PoolSize:         4,
			Threads:          0,
			MemoryLimit:      "2GB",
		},
		RateLimit: RateLimitConfig{
			QueriesPerMinute: 10,
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		path = os.Getenv("DS3SQL_CONFIG")
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			candidate := home + "/.ds3sql/server.yaml"
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	// Env var overrides
	if v := os.Getenv("DS3SQL_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("DS3SQL_IAM_URL"); v != "" {
		cfg.IAMURL = v
	}
	if v := os.Getenv("DS3SQL_DS3_GATEWAY_URL"); v != "" {
		cfg.DS3GatewayURL = v
	}
	if v := os.Getenv("DS3SQL_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Query.PoolSize = n
		}
	}
	if v := os.Getenv("DS3SQL_THREADS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Query.Threads = n
		}
	}
	if v := os.Getenv("DS3SQL_MEMORY_LIMIT"); v != "" {
		cfg.Query.MemoryLimit = v
	}

	return cfg, nil
}
