package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// MetastoreConfig holds settings for the embedded SQLite metastore.
type MetastoreConfig struct {
	Path string `yaml:"path"`
}

// StorageClassConfig maps a logical storage class to a real DS3 bucket + endpoint.
// An empty Endpoint means "use the session's gateway endpoint".
type StorageClassConfig struct {
	Bucket   string `yaml:"bucket"`
	Endpoint string `yaml:"endpoint"`
}

// StorageConfig holds the storage-class → bucket map used by the write path.
type StorageConfig struct {
	Classes map[string]StorageClassConfig `yaml:"classes"`
}

type Config struct {
	ListenAddr    string `yaml:"listen_addr"`
	IAMURL        string `yaml:"iam_url"`
	DS3GatewayURL string `yaml:"ds3_gateway_url"`
	Role          string `yaml:"role"`

	Auth      AuthConfig      `yaml:"auth"`
	Query     QueryConfig     `yaml:"query"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`

	Metastore MetastoreConfig `yaml:"metastore"`

	Storage StorageConfig `yaml:"storage"`
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
		Role:          "all",
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
		Metastore: MetastoreConfig{
			Path: defaultMetastorePath(),
		},
		Storage: StorageConfig{
			Classes: map[string]StorageClassConfig{
				"ssd": {Bucket: "ds3-fast", Endpoint: ""},
				"hdd": {Bucket: "ds3-cold", Endpoint: ""},
			},
		},
	}
}

func defaultMetastorePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "metastore.db"
	}
	return home + "/.ds3sql/metastore.db"
}

// ResolveStorageClass returns the configured bucket/endpoint for a logical class
// name (e.g. "ssd"/"hdd"). The bool is false when the class is not configured.
func (c *Config) ResolveStorageClass(name string) (StorageClassConfig, bool) {
	if c.Storage.Classes == nil {
		return StorageClassConfig{}, false
	}
	sc, ok := c.Storage.Classes[name]
	return sc, ok
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
	if v := os.Getenv("DS3SQL_ROLE"); v != "" {
		cfg.Role = v
	}
	if v := os.Getenv("DS3SQL_METASTORE_PATH"); v != "" {
		cfg.Metastore.Path = v
	}

	// Storage env overrides
	if v := os.Getenv("DS3SQL_STORAGE_SSD_BUCKET"); v != "" {
		if cfg.Storage.Classes == nil {
			cfg.Storage.Classes = make(map[string]StorageClassConfig)
		}
		s := cfg.Storage.Classes["ssd"]
		s.Bucket = v
		cfg.Storage.Classes["ssd"] = s
	}
	if v := os.Getenv("DS3SQL_STORAGE_SSD_ENDPOINT"); v != "" {
		if cfg.Storage.Classes == nil {
			cfg.Storage.Classes = make(map[string]StorageClassConfig)
		}
		s := cfg.Storage.Classes["ssd"]
		s.Endpoint = v
		cfg.Storage.Classes["ssd"] = s
	}
	if v := os.Getenv("DS3SQL_STORAGE_HDD_BUCKET"); v != "" {
		if cfg.Storage.Classes == nil {
			cfg.Storage.Classes = make(map[string]StorageClassConfig)
		}
		s := cfg.Storage.Classes["hdd"]
		s.Bucket = v
		cfg.Storage.Classes["hdd"] = s
	}
	if v := os.Getenv("DS3SQL_STORAGE_HDD_ENDPOINT"); v != "" {
		if cfg.Storage.Classes == nil {
			cfg.Storage.Classes = make(map[string]StorageClassConfig)
		}
		s := cfg.Storage.Classes["hdd"]
		s.Endpoint = v
		cfg.Storage.Classes["hdd"] = s
	}

	return cfg, nil
}
