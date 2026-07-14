// mistake-pr-db：VPC 内小 Lambda，替 GitHub Actions 在私有 RDS 上建/删 per-PR 逻辑库。
// 事件：{"action":"create"|"drop","pr":<int>}。master 连接串走 KMS 加密的 env MASTER_URL。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	Action string `json:"action"` // "create" | "drop"
	PR     int    `json:"pr"`
}

func handle(ctx context.Context, e Event) (string, error) {
	if e.PR <= 0 {
		return "", fmt.Errorf("bad pr: %d", e.PR)
	}
	db := fmt.Sprintf("mistake_pr_%d", e.PR)
	master := os.Getenv("MASTER_URL")
	if master == "" {
		return "", fmt.Errorf("MASTER_URL not set")
	}
	conn, err := pgx.Connect(ctx, master)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	// CREATE/DROP DATABASE 不能参数化库名；库名由 %d 拼接 + pgx.Identifier 转义，双重保险。
	ident := pgx.Identifier{db}.Sanitize()
	switch e.Action {
	case "create":
		var exists bool
		if err := conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", db).Scan(&exists); err != nil {
			return "", fmt.Errorf("check exists: %w", err)
		}
		if exists {
			return "exists " + db, nil
		}
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
			return "", fmt.Errorf("create: %w", err)
		}
		return "created " + db, nil
	case "drop":
		// 先踢掉残留连接再删
		_, _ = conn.Exec(ctx,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()", db)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident); err != nil {
			return "", fmt.Errorf("drop: %w", err)
		}
		return "dropped " + db, nil
	default:
		return "", fmt.Errorf("unknown action %q", e.Action)
	}
}

func main() { lambda.Start(handle) }
