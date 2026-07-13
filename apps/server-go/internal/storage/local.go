package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local 把图片写到本地目录，通过 <publicBase>/uploads/<name> 静态访问。
type Local struct {
	Dir        string
	PublicBase string
}

func NewLocal(dir, publicBase string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Local{Dir: dir, PublicBase: strings.TrimRight(publicBase, "/")}, nil
}

func (l *Local) Put(_ context.Context, name, _ string, r io.Reader) (string, error) {
	dst, err := os.Create(filepath.Join(l.Dir, name))
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, r); err != nil {
		return "", err
	}
	return l.PublicBase + uploadsPrefix + name, nil
}

func (l *Local) Get(_ context.Context, fileURL string) ([]byte, error) {
	name, err := baseName(fileURL)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(l.Dir, name))
}

func (l *Local) Delete(_ context.Context, fileURL string) error {
	name, err := baseName(fileURL)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(l.Dir, name))
}
