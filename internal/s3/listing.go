package s3

import (
	"context"
	"fmt"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type BucketInfo struct {
	Name         string `json:"name"`
	CreationDate string `json:"creation_date"`
}

type ObjectInfo struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified string `json:"last_modified"`
}

type ListResult struct {
	Prefixes    []string     `json:"prefixes"`
	Objects     []ObjectInfo `json:"objects"`
	IsTruncated bool         `json:"is_truncated"`
}

func (c *Client) ListBuckets(ctx context.Context) ([]BucketInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := c.client.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}

	buckets := make([]BucketInfo, 0, len(result.Buckets))
	for _, b := range result.Buckets {
		date := ""
		if b.CreationDate != nil {
			date = b.CreationDate.Format("2006-01-02T15:04:05Z")
		}
		buckets = append(buckets, BucketInfo{
			Name:         *b.Name,
			CreationDate: date,
		})
	}
	return buckets, nil
}

func (c *Client) ListObjects(ctx context.Context, bucket, prefix, delimiter string, maxKeys int32) (*ListResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if maxKeys <= 0 {
		maxKeys = 100
	}

	input := &awss3.ListObjectsV2Input{
		Bucket:  &bucket,
		Prefix:  &prefix,
		MaxKeys: &maxKeys,
	}

	if delimiter != "" {
		input.Delimiter = &delimiter
	}

	result, err := c.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	resp := ListResult{
		Prefixes: make([]string, 0),
		Objects:  make([]ObjectInfo, 0),
	}

	for _, cp := range result.CommonPrefixes {
		if cp.Prefix != nil {
			resp.Prefixes = append(resp.Prefixes, *cp.Prefix)
		}
	}

	for _, obj := range result.Contents {
		modified := ""
		if obj.LastModified != nil {
			modified = obj.LastModified.Format("2006-01-02T15:04:05Z")
		}
		size := int64(0)
		if obj.Size != nil {
			size = *obj.Size
		}
		resp.Objects = append(resp.Objects, ObjectInfo{
			Key:          *obj.Key,
			Size:         size,
			LastModified: modified,
		})
	}

	if result.IsTruncated != nil {
		resp.IsTruncated = *result.IsTruncated
	}

	return &resp, nil
}

// DeletePrefix deletes every object whose key starts with prefix. It paginates
// the listing with a continuation token and deletes objects one at a time
// (DS3-compatible; avoids the batch DeleteObjects API). Used to clear a managed
// table's location for overwrite loads and DROP.
func (c *Client) DeletePrefix(ctx context.Context, bucket, prefix string) error {
	var token *string
	for {
		out, err := c.client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            &bucket,
			Prefix:            &prefix,
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list for delete %q: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := c.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
				Bucket: &bucket,
				Key:    obj.Key,
			}); err != nil {
				return fmt.Errorf("delete %q: %w", *obj.Key, err)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			return nil
		}
		token = out.NextContinuationToken
	}
}
