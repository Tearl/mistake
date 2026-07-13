package main

import (
	"embed"
	"errors"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// runMigrations 启动时对目标库自动建表 + seed（迁移已嵌入二进制）。
// golang-migrate 自带 advisory lock，多副本并发启动安全；已是最新则无操作。
func runMigrations(databaseURL string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	// golang-migrate 的 pgx/v5 驱动注册的 scheme 是 pgx5://
	u := databaseURL
	for _, p := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(u, p) {
			u = "pgx5://" + strings.TrimPrefix(u, p)
			break
		}
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, u)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}
