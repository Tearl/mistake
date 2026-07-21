package perf

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Source 是清洗任务的事件来源（CloudWatch 或本地文件）。
type Source interface {
	Fetch(ctx context.Context, start, end int64) ([]Event, error)
}

// fileSource 从本地 JSONL 读回窗口内事件（开发用）。
type fileSource struct{ dir string }

func NewFileSource(dir string) Source { return &fileSource{dir: dir} }

func (f *fileSource) Fetch(_ context.Context, start, end int64) ([]Event, error) {
	var out []Event
	// 窗口可能跨 UTC 日界，读今天与昨天两个文件即可覆盖常见 5~15 分钟窗口。
	days := []string{
		time.UnixMilli(end).UTC().Format("20060102"),
		time.UnixMilli(start).UTC().Format("20060102"),
	}
	seen := map[string]bool{}
	for _, d := range days {
		if seen[d] {
			continue
		}
		seen[d] = true
		fh, err := os.Open(filepath.Join(f.dir, "perf-"+d+".jsonl"))
		if err != nil {
			continue // 文件不存在就跳过
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.TS >= start && e.TS <= end {
				out = append(out, e)
			}
		}
		fh.Close()
	}
	return out, nil
}

// SummaryStore 存/取清洗产物（S3 或本地文件）。
type SummaryStore interface {
	Put(ctx context.Context, s Summary) error
	Get(ctx context.Context) (Summary, bool, error)
}

// localStore 把 latest.json 落到本地目录（开发用）。
type localStore struct{ path string }

func NewLocalStore(dir string) SummaryStore {
	_ = os.MkdirAll(dir, 0o755)
	return &localStore{path: filepath.Join(dir, "latest.json")}
}

func (l *localStore) Put(_ context.Context, s Summary) error {
	raw, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(l.path, raw, 0o644)
}

func (l *localStore) Get(_ context.Context) (Summary, bool, error) {
	raw, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return Summary{}, false, nil
	}
	if err != nil {
		return Summary{}, false, err
	}
	var s Summary
	return s, true, json.Unmarshal(raw, &s)
}

// s3Store 把 latest.json 落到 S3（生产）。
type s3Store struct {
	client *s3.Client
	bucket string
	key    string
}

func NewS3Store(client *s3.Client, bucket, key string) SummaryStore {
	return &s3Store{client: client, bucket: bucket, key: key}
}

func (s *s3Store) Put(ctx context.Context, sum Summary) error {
	raw, _ := json.Marshal(sum)
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &s.key,
		Body:        bytes.NewReader(raw),
		ContentType: aws.String("application/json"),
	})
	return err
}

func (s *s3Store) Get(ctx context.Context) (Summary, bool, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &s.key})
	if err != nil {
		var nsk *s3types.NoSuchKey
		if errors.As(err, &nsk) {
			return Summary{}, false, nil
		}
		return Summary{}, false, err
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(out.Body)
	if err != nil {
		return Summary{}, false, err
	}
	var sum Summary
	return sum, true, json.Unmarshal(raw, &sum)
}

// RunAggregation 清洗一次：取近 windowMinutes 分钟事件 → 聚合 → 存 latest。
func RunAggregation(ctx context.Context, src Source, store SummaryStore, windowMinutes int) (Summary, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	end := time.Now().UnixMilli()
	start := end - int64(windowMinutes)*60000
	events, err := src.Fetch(ctx, start, end)
	if err != nil {
		return Summary{}, err
	}
	sum := Aggregate(events, start, end)
	if err := store.Put(ctx, sum); err != nil {
		return Summary{}, err
	}
	return sum, nil
}
