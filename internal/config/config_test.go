package config

import (
	"os"
	"testing"
)

func TestDefault_RoleAndMetastore(t *testing.T) {
	c := Default()
	if c.Role != "all" {
		t.Fatalf("expected default role 'all', got %q", c.Role)
	}
	if c.Metastore.Path == "" {
		t.Fatal("expected a default metastore path")
	}
}

func TestLoad_MetastoreEnvOverride(t *testing.T) {
	t.Setenv("DS3SQL_METASTORE_PATH", "/tmp/custom-meta.db")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Metastore.Path != "/tmp/custom-meta.db" {
		t.Fatalf("env override not applied: %q", c.Metastore.Path)
	}
	_ = os.Unsetenv("DS3SQL_METASTORE_PATH")
}

func TestDefault_StorageClasses(t *testing.T) {
	c := Default()
	ssd, ok := c.ResolveStorageClass("ssd")
	if !ok {
		t.Fatal("expected default ssd storage class")
	}
	if ssd.Bucket == "" {
		t.Fatal("expected default ssd bucket")
	}
	if _, ok := c.ResolveStorageClass("hdd"); !ok {
		t.Fatal("expected default hdd storage class")
	}
	if _, ok := c.ResolveStorageClass("nope"); ok {
		t.Fatal("unknown class must not resolve")
	}
}

func TestLoad_StorageEnvOverride(t *testing.T) {
	t.Setenv("DS3SQL_STORAGE_SSD_BUCKET", "fast-bucket")
	t.Setenv("DS3SQL_STORAGE_SSD_ENDPOINT", "https://ssd.example")
	t.Setenv("DS3SQL_STORAGE_HDD_BUCKET", "cold-bucket")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ssd, ok := c.ResolveStorageClass("ssd")
	if !ok || ssd.Bucket != "fast-bucket" || ssd.Endpoint != "https://ssd.example" {
		t.Fatalf("ssd env override not applied: %+v", ssd)
	}
	hdd, _ := c.ResolveStorageClass("hdd")
	if hdd.Bucket != "cold-bucket" {
		t.Fatalf("hdd env override not applied: %+v", hdd)
	}
}

func TestDefault_Phase2Sections(t *testing.T) {
	c := Default()
	if c.Query.MaxConcurrentPerProject <= 0 {
		t.Fatal("expected a positive default max_concurrent_per_project")
	}
	if c.Cache.ResultTTL <= 0 {
		t.Fatal("expected a default result cache TTL")
	}
	if c.Cache.ResultDir == "" || c.Cache.DataDir == "" {
		t.Fatal("expected default cache dirs")
	}
}

func TestLoad_ClusterEnvOverrides(t *testing.T) {
	t.Setenv("DS3SQL_CLUSTER_WORKERS", "http://w1:8080,http://w2:8080")
	t.Setenv("DS3SQL_CLUSTER_SHARED_SECRET", "sekret")
	t.Setenv("DS3SQL_MAX_CONCURRENT_PER_PROJECT", "5")
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Cluster.Workers) != 2 || c.Cluster.Workers[0] != "http://w1:8080" {
		t.Fatalf("workers env override not applied: %+v", c.Cluster.Workers)
	}
	if c.Cluster.SharedSecret != "sekret" {
		t.Fatalf("shared secret env override not applied: %q", c.Cluster.SharedSecret)
	}
	if c.Query.MaxConcurrentPerProject != 5 {
		t.Fatalf("max_concurrent env override not applied: %d", c.Query.MaxConcurrentPerProject)
	}
}
