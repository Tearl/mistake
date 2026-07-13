# server-go —— 拾错 Go 后端

用 Go 实现的真实后端，取代了原微信小程序的 7 个云开发云函数，也替换了脚手架自带的 TS(Hono/tRPC) server。前端 `apps/web` 通过 REST 调用它。

- 路由：`chi`
- 数据库：PostgreSQL，`pgx` + `sqlc`（`golang-migrate` 管理 schema）
- AI：通义千问 DashScope OpenAI 兼容接口（`recognize` 用 `qwen-vl-plus`，`similar` 用 `qwen-plus`）
- 图片：上传到本地磁盘 `uploads/`，通过 `/uploads/*` 静态访问
- 鉴权：**暂无**，单用户模式，所有数据挂在固定 `dev-user`（seed 为 admin）下

## 依赖（macOS / Homebrew）

```bash
brew install go sqlc golang-migrate postgresql@16
brew services start postgresql@16
createdb mistake
```

## 环境变量（`.env`）

```
DATABASE_URL=postgres://<你的用户名>@localhost:5432/mistake?sslmode=disable
DASHSCOPE_API_KEY=            # 阿里云百炼 key；留空则 /api/recognize、/api/similar 返回 503
CORS_ORIGIN=http://localhost:3001
PORT=3000
UPLOAD_DIR=uploads
PUBLIC_BASE_URL=http://localhost:3000
```

## 建表 & 运行

```bash
# 建表 + seed dev 用户
migrate -path migrations -database "$DATABASE_URL" up

# 改了 queries/ 或 migrations/ 后重新生成 sqlc 代码
sqlc generate

# 启动（:3000）
go run .
```

## REST 接口（前缀 `/api`）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/mistakes?subject=&limit=` | 错题列表 |
| POST | `/mistakes` | 新增错题（上传保存 / 变式题入库） |
| GET | `/mistakes/{id}` | 详情 |
| PATCH | `/mistakes/{id}` | 改掌握状态 `{mastery}` |
| POST | `/mistakes/{id}/grade` | 复习评分 `{action: unknown/fuzzy/mastered}` |
| DELETE | `/mistakes/{id}` | 删除（连带删本地图片） |
| GET | `/random?subject=&mastery=` | 随机抽一题 + 题池数量 |
| GET | `/stats` | 统计：数量 / 学科分布 / 本周柱状图 / 连续天数 |
| GET | `/admin` | 管理后台聚合 |
| POST | `/upload` | multipart 图片上传，返回 `{imageFileID}` |
| POST | `/recognize` | `{imageFileID}` → 通义千问识别错题 |
| POST | `/similar` | 举一反三生成变式题 |
| POST | `/export` | `{subject, ids}` → 直接下载 .docx |
