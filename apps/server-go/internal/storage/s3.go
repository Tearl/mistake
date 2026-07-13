package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3 把图片存到 S3 桶的 uploads/ 前缀下。凭证走默认链（ECS Task Role），代码里不写密钥。
type S3 struct {
	client     *s3.Client
	bucket     string
	publicBase string // 图片公开前缀，如 https://<bucket>.s3.<region>.amazonaws.com
}

func NewS3(ctx context.Context, region, bucket, publicBase string) (*S3, error) {
	if bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required for s3 storage")
	}
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	if publicBase == "" {
		publicBase = fmt.Sprintf("https://%s.s3.%s.amazonaws.com", bucket, cfg.Region)
	}
	return &S3{
		client:     s3.NewFromConfig(cfg),
		bucket:     bucket,
		publicBase: strings.TrimRight(publicBase, "/"),
	}, nil
}

func (s *S3) key(name string) string { return "uploads/" + name }

func (s *S3) Put(ctx context.Context, name, contentType string, r io.Reader) (string, error) {
	// S3 PutObject 需要可 seek 的 body，先读进内存（错题图不大）
	buf, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	key := s.key(name)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(buf),
		ContentType: &contentType,
	})
	if err != nil {
		return "", err
	}
	return s.publicBase + uploadsPrefix + name, nil
}

func (s *S3) Get(ctx context.Context, fileURL string) ([]byte, error) {
	name, err := baseName(fileURL)
	if err != nil {
		return nil, err
	}
	key := s.key(name)
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (s *S3) Delete(ctx context.Context, fileURL string) error {
	name, err := baseName(fileURL)
	if err != nil {
		return err
	}
	key := s.key(name)
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}
