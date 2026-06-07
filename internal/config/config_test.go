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
