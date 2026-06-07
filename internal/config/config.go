package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MetastoreConfig holds settings for the embedded SQLite metastore.
type MetastoreConfig struct {
	Path string `yaml:"path"`
}

// ClusterConfig configures the static worker pool and coordinator↔worker auth.
type ClusterConfig struct {
	Workers      []string `yaml:"workers"`       // worker base URLs, e.g. http://w1:8080
	SharedSecret string   `yaml:"shared_secret"` // guards /internal/execute
}

// CacheConfig configures the result cache and the worker local-SSD data cache.
type CacheConfig struct {
	ResultDir      string        `yaml:"result_dir"`       // payload dir (or SSD-bucket mount)
	ResultTTL      time.Duration `yaml:"result_ttl"`
	ResultMaxBytes int64         `yaml:"result_max_bytes"`
	DataDir        string        `yaml:"data_dir"`         // worker SSD cache dir
	DataMaxBytes   int64         `yaml:"data_max_bytes"`
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

	Cluster ClusterConfig `yaml:"cluster"`
	Cache   CacheConfig   `yaml:"cache"`

	Storage StorageConfig `yaml:"storage"`
}

type AuthConfig struct {
	TokenExpiry        time.Duration `yaml:"token_expiry"`
	RefreshTokenExpiry time.Duration `yaml:"refresh_token_expiry"`
}

type QueryConfig struct {
	MaxRows                  int    `yaml:"max_rows"`
	MaxExecutionSecs         int    `yaml:"max_execution_seconds"`
	MaxResultBytes           int64  `yaml:"max_result_bytes"`
	PoolSize                 int    `yaml:"pool_size"`
	Threads                  int    `yaml:"threads"`
	MemoryLimit              string `yaml:"memory_limit"`
	MaxConcurrentPerProject  int    `yaml:"max_concurrent_per_project"`
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
			MaxRows:                  10000,
			MaxExecutionSecs:         60,
			MaxResultBytes:           104857600,
			PoolSize:                 4,
			Threads:                  0,
			MemoryLimit:              "2GB",
			MaxConcurrentPerProject:  4,
		},
		RateLimit: RateLimitConfig{
			QueriesPerMinute: 10,
		},
		Metastore: MetastoreConfig{
			Path: defaultMetastorePath(),
		},
		Cluster: ClusterConfig{
			Workers:      nil,
			SharedSecret: "",
		},
		Cache: CacheConfig{
			ResultDir:      defaultCacheDir("results"),
			ResultTTL:      1 * time.Hour,
			ResultMaxBytes: 10 << 30, // 10 GiB
			DataDir:        defaultCacheDir("data"),
			DataMaxBytes:   50 << 30, // 50 GiB
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

func defaultCacheDir(sub string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("cache", sub)
	}
	return filepath.Join(home, ".ds3sql", "cache", sub)
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

	// Cluster env overrides
	if v := os.Getenv("DS3SQL_CLUSTER_WORKERS"); v != "" {
		parts := strings.Split(v, ",")
		workers := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				workers = append(workers, p)
			}
		}
		cfg.Cluster.Workers = workers
	}
	if v := os.Getenv("DS3SQL_CLUSTER_SHARED_SECRET"); v != "" {
		cfg.Cluster.SharedSecret = v
	}
	if v := os.Getenv("DS3SQL_MAX_CONCURRENT_PER_PROJECT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Query.MaxConcurrentPerProject = n
		}
	}

	// Cache env overrides
	if v := os.Getenv("DS3SQL_CACHE_RESULT_DIR"); v != "" {
		cfg.Cache.ResultDir = v
	}
	if v := os.Getenv("DS3SQL_CACHE_RESULT_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Cache.ResultTTL = d
		}
	}
	if v := os.Getenv("DS3SQL_CACHE_RESULT_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Cache.ResultMaxBytes = n
		}
	}
	if v := os.Getenv("DS3SQL_CACHE_DATA_DIR"); v != "" {
		cfg.Cache.DataDir = v
	}
	if v := os.Getenv("DS3SQL_CACHE_DATA_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.Cache.DataMaxBytes = n
		}
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
