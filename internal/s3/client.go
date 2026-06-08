package s3

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	client   *awss3.Client
	endpoint string
}

func NewClient(ctx context.Context, accessKey, secretKey, endpoint string) (*Client, error) {
	// Ensure endpoint has protocol for AWS SDK
	s3Endpoint := endpoint
	if !strings.HasPrefix(s3Endpoint, "http://") && !strings.HasPrefix(s3Endpoint, "https://") {
		s3Endpoint = "https://" + s3Endpoint
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		config.WithRegion("us-east-1"),
	)
	if err != nil {
		return nil, err
	}

	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(s3Endpoint)
		o.UsePathStyle = true
	})

	return &Client{client: client, endpoint: s3Endpoint}, nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := c.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	return err
}

func (c *Client) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := c.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return out.Body, nil
}

// CopyObject copies an object from source bucket/key to destination bucket/key.
func (c *Client) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	src := "/" + srcBucket + "/" + srcKey
	_, err := c.client.CopyObject(ctx, &awss3.CopyObjectInput{
		Bucket:     &dstBucket,
		CopySource: &src,
		Key:        &dstKey,
	})
	return err
}
