package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"mistakeserver/internal/ai"
	"mistakeserver/internal/config"
	"mistakeserver/internal/db"
	"mistakeserver/internal/handlers"
	"mistakeserver/internal/messaging"
	"mistakeserver/internal/perf"
	"mistakeserver/internal/recognition"
	"mistakeserver/internal/storage"
	"mistakeserver/internal/worker"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 清洗任务：读性能日志聚合成 /ops 产物后退出，不需要数据库。
	if cfg.AppMode == "perfagg" {
		if err := runPerfAggregation(ctx, cfg); err != nil {
			log.Fatalf("perf aggregation: %v", err)
		}
		return
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	if cfg.AppMode == "migrate" || cfg.AutoMigrate {
		if err := runMigrations(cfg.DatabaseURL); err != nil {
			log.Fatalf("run migrations: %v", err)
		}
	}
	if cfg.AppMode == "migrate" {
		log.Print("database migrations completed")
		return
	}

	// 选择图片存储：s3（生产）或 local（开发）
	store, err := newStorage(ctx, cfg)
	if err != nil {
		log.Fatalf("init storage: %v", err)
	}

	aiClient := ai.New(cfg.DashScopeKey)
	queries := db.New(pool)
	processor := recognition.New(queries, aiClient, store)

	var publisher messaging.Publisher
	if cfg.AsyncRecognition && (cfg.AppMode == "api" || cfg.AppMode == "all") {
		publisher, err = messaging.NewSNSPublisher(ctx, cfg.AWSRegion, cfg.SNSTopicARN)
		if err != nil {
			log.Fatalf("init SNS publisher: %v", err)
		}
	}

	if cfg.AppMode == "worker" || cfg.AppMode == "all" {
		recognitionWorker, workerErr := worker.NewRecognitionWorker(
			ctx, cfg.AWSRegion, cfg.SQSQueueURL,
			cfg.SQSWaitTimeSeconds, cfg.SQSVisibilityTimeout, cfg.SQSMaxReceiveCount,
			processor, queries,
		)
		if workerErr != nil {
			log.Fatalf("init recognition worker: %v", workerErr)
		}
		if cfg.AppMode == "worker" {
			if err := recognitionWorker.Run(ctx); err != nil {
				log.Fatal(err)
			}
			return
		}
		go func() {
			if err := recognitionWorker.Run(ctx); err != nil {
				log.Printf("recognition worker stopped: %v", err)
				stop()
			}
		}()
	}

	if cfg.AppMode != "api" && cfg.AppMode != "all" {
		log.Fatalf("unsupported APP_MODE %q", cfg.AppMode)
	}

	opsStore, err := newOpsStore(ctx, cfg)
	if err != nil {
		log.Fatalf("init ops store: %v", err)
	}
	srv := handlers.New(cfg, pool, aiClient, store, publisher, processor, opsStore)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(cfg.CORSOrigins()))

	// 性能 SDK：中间件采集每请求指标，后台每 5 秒批量落地。
	if cfg.PerfEnabled {
		sink, sinkErr := newPerfSink(ctx, cfg)
		if sinkErr != nil {
			log.Fatalf("init perf sink: %v", sinkErr)
		}
		collector := perf.NewCollector(sink, time.Duration(cfg.PerfFlushSeconds)*time.Second)
		r.Use(perf.Middleware(collector))
		go collector.Run(ctx)
	}

	// Liveness does not touch dependencies; readiness verifies the database.
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	ready := func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"not_ready"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
	r.Get("/health", ready)
	r.Get("/health/ready", ready)
	r.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-App-Version", cfg.AppVersion)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version": cfg.AppVersion, "buildTime": cfg.BuildTime, "mode": cfg.AppMode,
		})
	})

	r.Route("/api", func(r chi.Router) {
		r.Use(apiKeyMiddleware(cfg.APIKey))
		r.Get("/mistakes", srv.ListMistakes)
		r.Post("/mistakes", srv.CreateMistake)
		r.Get("/mistakes/{id}", srv.GetMistake)
		r.Patch("/mistakes/{id}", srv.UpdateMastery)
		r.Post("/mistakes/{id}/grade", srv.GradeMistake)
		r.Delete("/mistakes/{id}", srv.DeleteMistake)
		r.Get("/random", srv.Random)
		r.Get("/stats", srv.Stats)
		r.Get("/admin", srv.Admin)
		r.Post("/upload", srv.Upload)
		r.Post("/recognize", srv.Recognize)
		r.Post("/recognitions", srv.CreateRecognition)
		r.Get("/recognitions/{id}", srv.GetRecognition)
		r.Post("/similar", srv.Similar)
		r.Post("/export", srv.Export)
		r.Get("/cloudmap-hello", srv.CloudMapHello) // 作业二：ECS 经 Cloud Map 发现并调 Lambda
		r.Get("/ops/summary", srv.OpsSummary)       // 性能面板：读清洗任务最新聚合
	})

	// 本地模式：静态提供上传的图片（s3 模式由 S3 直接提供，无此路由）
	if cfg.Storage != "s3" {
		r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(cfg.UploadDir))))
	}

	addr := ":" + cfg.Port
	server := &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("拾错 Go 后端启动于 :%s（storage=%s, AI key=%v, apiKey=%v, async=%v, version=%s）",
		cfg.Port, cfg.Storage, srv.AI.HasKey(), cfg.APIKey != "", cfg.AsyncRecognition, cfg.AppVersion)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newStorage(ctx context.Context, cfg *config.Config) (storage.Storage, error) {
	if cfg.Storage == "s3" {
		return storage.NewS3(ctx, cfg.AWSRegion, cfg.S3Bucket, cfg.S3PublicBaseURL)
	}
	return storage.NewLocal(cfg.UploadDir, cfg.PublicBaseURL)
}

// 共享密钥：API_KEY 非空时要求请求头 X-API-Key 匹配（预检 OPTIONS 放行）
func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if key != "" && r.Method != http.MethodOptions && r.Header.Get("X-API-Key") != key {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func corsMiddleware(allow []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allow))
	for _, o := range allow {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-API-Key")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
