// Package storage 抽象错题图片的存放：本地磁盘（开发）或 S3（生产）。
package storage

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
)

// Storage 存/取/删一张图片。
// Put 的 name 是文件名（如 ab12.jpg），返回可公开访问的 URL。
// Get/Delete 接收之前 Put 返回的 URL。
type Storage interface {
	Put(ctx context.Context, name, contentType string, r io.Reader) (url string, err error)
	Get(ctx context.Context, fileURL string) ([]byte, error)
	Delete(ctx context.Context, fileURL string) error
}

// 所有图片 URL 都形如 <base>/uploads/<name>，两种实现共用它反推文件名。
const uploadsPrefix = "/uploads/"

func baseName(fileURL string) (string, error) {
	idx := strings.LastIndex(fileURL, uploadsPrefix)
	if idx == -1 {
		return "", errors.New("not an uploaded image url")
	}
	name := path.Base(fileURL[idx+len(uploadsPrefix):])
	if name == "" || name == "." || strings.Contains(name, "..") {
		return "", errors.New("invalid image name")
	}
	return name, nil
}
