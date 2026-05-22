package s3

import (
	"context"
	"os"
	"testing"
)

func skipIfNoCreds(t *testing.T) {
	t.Helper()
	if os.Getenv("DS3SQL_TEST_ACCESS_KEY") == "" {
		t.Skip("set DS3SQL_TEST_ACCESS_KEY/DS3SQL_TEST_SECRET_KEY/DS3SQL_TEST_ENDPOINT for integration tests")
	}
}

func TestNewClient(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx, "test-key", "test-secret", "http://localhost:9000")
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestListBucketsIntegration(t *testing.T) {
	skipIfNoCreds(t)

	ctx := context.Background()
	client, err := NewClient(ctx,
		os.Getenv("DS3SQL_TEST_ACCESS_KEY"),
		os.Getenv("DS3SQL_TEST_SECRET_KEY"),
		os.Getenv("DS3SQL_TEST_ENDPOINT"),
	)
	if err != nil {
		t.Fatal(err)
	}

	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("found %d buckets", len(buckets))
	for _, b := range buckets {
		t.Logf("  %s (%s)", b.Name, b.CreationDate)
	}
}
