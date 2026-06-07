package s3

import (
	"context"
	"testing"
)

func TestDeletePrefix_UnreachableEndpointReturnsError(t *testing.T) {
	// 127.0.0.1:1 is reserved/closed; the SDK should fail fast, proving the
	// method is wired and does not panic on the list step.
	c, err := NewClient(context.Background(), "ak", "sk", "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.DeletePrefix(context.Background(), "bucket", "prefix/"); err == nil {
		t.Fatal("expected error against unreachable endpoint")
	}
}
