package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// 单用户模式：所有数据都挂在这个固定用户下（migrations 里已 seed，role=admin）
const DevUserID = "dev-user"

type Config struct {
	DatabaseURL  string
	DashScopeKey string
	CORSOrigin   string // 逗号分隔的允许 origin 白名单
	Port         string
	APIKey       string // 非空则要求请求头 X-API-Key 匹配

	// 图片存储
	Storage         string // local | s3
	UploadDir       string // local 用
	PublicBaseURL   string // local 用：拼图片 URL
	S3Bucket        string // s3 用
	AWSRegion       string // s3 用
	S3PublicBaseURL string // s3 用：图片公开访问前缀，留空则用 https://<bucket>.s3.<region>.amazonaws.com
}

func Load() *Config {
	// 就近加载 .env（存在才加载，缺失不报错）
	_ = godotenv.Load(".env")

	c := &Config{
		DatabaseURL:     env("DATABASE_URL", "postgres://localhost:5432/mistake?sslmode=disable"),
		DashScopeKey:    env("DASHSCOPE_API_KEY", ""),
		CORSOrigin:      env("CORS_ORIGIN", "http://localhost:3001"),
		Port:            env("PORT", "3000"),
		APIKey:          env("API_KEY", ""),
		Storage:         env("STORAGE", "local"),
		UploadDir:       env("UPLOAD_DIR", "uploads"),
		PublicBaseURL:   env("PUBLIC_BASE_URL", "http://localhost:3000"),
		S3Bucket:        env("S3_BUCKET", ""),
		AWSRegion:       env("AWS_REGION", ""),
		S3PublicBaseURL: env("S3_PUBLIC_BASE_URL", ""),
	}
	if abs, err := filepath.Abs(c.UploadDir); err == nil {
		c.UploadDir = abs
	}
	return c
}

// CORSOrigins 返回解析后的白名单
func (c *Config) CORSOrigins() []string {
	var out []string
	for _, o := range strings.Split(c.CORSOrigin, ",") {
		if s := strings.TrimSpace(o); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
