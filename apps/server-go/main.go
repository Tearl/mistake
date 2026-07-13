package main

import (
	"context"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"mistakeserver/internal/ai"
	"mistakeserver/internal/config"
	"mistakeserver/internal/handlers"
	"mistakeserver/internal/storage"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	// 启动时自动建表 + seed（迁移已嵌入二进制）
	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	// 选择图片存储：s3（生产）或 local（开发）
	store, err := newStorage(ctx, cfg)
	if err != nil {
		log.Fatalf("init storage: %v", err)
	}

	srv := handlers.New(cfg, pool, ai.New(cfg.DashScopeKey), store)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(cfg.CORSOrigins()))

	// 健康检查：ALB Target Group 打它，不走鉴权
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
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
		r.Post("/similar", srv.Similar)
		r.Post("/export", srv.Export)
	})

	// 本地模式：静态提供上传的图片（s3 模式由 S3 直接提供，无此路由）
	if cfg.Storage != "s3" {
		r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(cfg.UploadDir))))
	}

	addr := ":" + cfg.Port
	log.Printf("拾错 Go 后端启动于 :%s（storage=%s, AI key=%v, apiKey=%v）",
		cfg.Port, cfg.Storage, srv.AI.HasKey(), cfg.APIKey != "")
	if err := http.ListenAndServe(addr, r); err != nil {
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
