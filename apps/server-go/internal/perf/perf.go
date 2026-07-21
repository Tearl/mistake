// Package perf 是轻量性能 SDK：chi 中间件按请求采集 {路由,方法,状态,延迟}，
// 每 5 秒批量写出。生产写 CloudWatch Logs 专用日志组，本地写 JSONL 文件，
// 页面/清洗任务再据此聚合统计。对主流程零阻塞：采集只入内存缓冲，落盘在后台。
package perf

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Event 是一条请求性能记录。字段用短名压日志体积。
type Event struct {
	TS        int64  `json:"t"` // 毫秒时间戳
	Route     string `json:"r"` // chi 路由模板，如 /api/mistakes/{id}
	Method    string `json:"m"`
	Status    int    `json:"s"`
	LatencyMs int64  `json:"l"`
}

// Sink 是批量落地目标（CloudWatch 或本地文件）。
type Sink interface {
	Write(ctx context.Context, events []Event) error
}

// Collector 缓冲事件并定时冲刷。
type Collector struct {
	mu    sync.Mutex
	buf   []Event
	sink  Sink
	every time.Duration
	max   int
}

func NewCollector(sink Sink, every time.Duration) *Collector {
	if every <= 0 {
		every = 5 * time.Second
	}
	return &Collector{sink: sink, every: every, max: 10000}
}

func (c *Collector) record(e Event) {
	c.mu.Lock()
	if len(c.buf) < c.max { // 背压：缓冲溢出时丢弃，绝不阻塞请求
		c.buf = append(c.buf, e)
	}
	c.mu.Unlock()
}

func (c *Collector) drain() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) == 0 {
		return nil
	}
	out := c.buf
	c.buf = make([]Event, 0, 128)
	return out
}

// Run 每 every 冲刷一次，直到 ctx 结束；退出前再冲一次。
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(c.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.flush(context.Background())
			return
		case <-t.C:
			c.flush(ctx)
		}
	}
}

func (c *Collector) flush(ctx context.Context) {
	events := c.drain()
	if len(events) == 0 {
		return
	}
	if err := c.sink.Write(ctx, events); err != nil {
		log.Printf("event=perf_flush_failed count=%d error=%q", len(events), err)
	}
}

// statusRecorder 捕获响应状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Middleware 采集每请求性能。路由模板在处理后才确定，故在 next 之后读取。
func Middleware(c *Collector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			route := chi.RouteContext(r.Context()).RoutePattern()
			if route == "" {
				route = "unmatched"
			}
			c.record(Event{
				TS:        start.UnixMilli(),
				Route:     route,
				Method:    r.Method,
				Status:    rec.status,
				LatencyMs: time.Since(start).Milliseconds(),
			})
		})
	}
}

// ---- 本地文件 Sink（开发用，无 AWS 也能跑通全链路） ----

type fileSink struct {
	dir string
	mu  sync.Mutex
}

// NewFileSink 把事件按天追加为 JSONL：<dir>/perf-YYYYMMDD.jsonl
func NewFileSink(dir string) Sink {
	_ = os.MkdirAll(dir, 0o755)
	return &fileSink{dir: dir}
}

func (f *fileSink) Write(_ context.Context, events []Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := filepath.Join(f.dir, "perf-"+time.Now().UTC().Format("20060102")+".jsonl")
	fh, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	enc := json.NewEncoder(fh)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
