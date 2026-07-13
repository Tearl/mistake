# 拾错 · 错题本（mistake）

拍照上传错题 → AI（通义千问-VL）识别归类 → 入库 → 随机抽题复习 → 学习统计。
由微信小程序 `mistake-mini` 移植为 Web：前端 React，后端 Go。

- **前端** `apps/web`：React + TanStack Router + TailwindCSS（neo-brutalist 风），shadcn/ui 在 `packages/ui`
- **后端** `apps/server-go`：Go + chi + pgx/sqlc + PostgreSQL，AI 走通义千问 DashScope，图片存本地磁盘
- 数据层用 REST 对接（前端 `src/lib/api.ts`）。**暂无鉴权**，单用户模式（固定 `dev-user`）

> 脚手架自带的 TS 后端（`apps/server` Hono/tRPC）与 `packages/{api,auth,db}` 已删除，被 `apps/server-go` 取代。

## 快速开始

### 1. 前端依赖

```bash
npm install
```

### 2. 后端（Go）—— 见 `apps/server-go/README.md`

```bash
brew install go sqlc golang-migrate postgresql@16
brew services start postgresql@16
createdb mistake

cd apps/server-go
# 按需修改 .env（DATABASE_URL 的用户名、DASHSCOPE_API_KEY）
migrate -path migrations -database "$DATABASE_URL" up   # 建表 + seed dev 用户
go run .                                                # 启动 :3000
```

> AI 识别/举一反三需要 `DASHSCOPE_API_KEY`（阿里云百炼）。留空时这两个接口返回 503，其余功能（CRUD/抽题/统计/导出）正常。

### 3. 前端

```bash
cd apps/web
npm run dev:bare        # 或在根目录 npm run dev:web
```

打开 [http://localhost:3001](http://localhost:3001)，后端在 [http://localhost:3000](http://localhost:3000)。

## 项目结构

```
mistake/
├── apps/
│   ├── web/         # 前端（React + TanStack Router），src/lib/api.ts 调用 Go 后端
│   └── server-go/   # Go 后端（chi + pgx/sqlc + Postgres + 通义千问 + 本地存图）
├── packages/
│   ├── ui/          # 共享 shadcn/ui 组件与样式
│   ├── env/         # 环境变量校验（web 用 VITE_SERVER_URL）
│   ├── config/      # 共享 tsconfig
│   └── infra/       # Cloudflare 部署（alchemy）
```

## UI 定制

- 全局设计 token / 样式：`packages/ui/src/styles/globals.css`
- 品牌配色（拾错的 neo-brutalist 大色块）：`apps/web/src/styles/mistake.css` + `apps/web/src/lib/theme.ts`
- 共享组件：`packages/ui/src/components/*`，引入方式 `import { Button } from "@mistake/ui/components/button"`
