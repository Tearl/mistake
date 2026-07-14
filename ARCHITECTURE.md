# 拾错 · 架构说明书

错题本 Web 应用（微信小程序 `mistake-mini` 移植）。前端 React，后端 Go，跑在 Cloudflare Pages + AWS（ECS/ALB/RDS/S3）+ 阿里云 DashScope。本文说清三条链路：**前端开发→部署**、**后端 Go 开发→部署**、**PR 从开到销毁的全过程**。

配套文档：生产手动部署见 [deploy/aws/RUNBOOK.md](deploy/aws/RUNBOOK.md)；PR 预览基建见 [deploy/aws/pr-preview/README.md](deploy/aws/pr-preview/README.md) 与 [deploy/aws/pr-preview/ALT-github-actions.md](deploy/aws/pr-preview/ALT-github-actions.md)。

---

## 0. 总览

```
                    ┌───────────── 运行时（两套并存，共享基础设施）─────────────┐
用户浏览器 ─┬─► Cloudflare Pages 生产  app.toton123.xyz ─┐
           └─► Cloudflare Pages 预览  <slug>.pages.dev ─┤ REST + X-API-Key
                                                        ▼
                              AWS ALB（443 · 通配证书 *.api）
                              ├ default 规则         → 生产 TG → ECS 生产服务 mistake(arm64)
                              └ host pr-N.api 规则    → 预览 TG → ECS 预览服务 mistake-pr-N(x86)
                                                        │  （ECS 从 ECR 拉镜像）
                              共享：RDS(生产库+pr-N 库) · S3 uploads · SSM 密钥 · DashScope(外部)

                    └───────────── 交付 ─────────────┐
生产后端/前端 = 手动（RUNBOOK）              预览(每 PR) = GitHub Actions + OIDC + VPC Lambda
```

**关键区分**：**生产**目前仍是**人工按 RUNBOOK 部署**（`docker build/push`+`ecs update-service`；`wrangler pages deploy --branch main`）。**只有 PR 预览环境接了自动化**（GitHub Actions）。两者复用同一套 ALB / ECS 集群 / RDS / S3 / ECR。

单用户模式、无鉴权：所有数据挂固定 `dev-user`（见 `apps/server-go/internal/config` 的 `DevUserID`）；`/api/*` 用共享 `X-API-Key` 挡随手直调。

---

## 1. 前端链路：开发 → 部署

代码：`apps/web`（React + TanStack Router，纯 REST，无 tRPC/鉴权）。数据层 `apps/web/src/lib/api.ts`（带 `X-API-Key`）。构建期配置在 `packages/env`（`VITE_SERVER_URL`、`VITE_API_KEY`）。SPA 深链回退 `apps/web/public/_redirects`（`/* /index.html 200`）。

### 1.1 本地开发
```bash
cd apps/web && npm run dev:bare      # :3001，打本地后端 :3000
```
后端未起时接口报错属正常。`VITE_SERVER_URL` 默认指向本地 `http://localhost:3000`。

### 1.2 构建（关键：URL/KEY 是构建期注入）
```bash
VITE_SERVER_URL=<后端地址> VITE_API_KEY=<共享密钥> npm run build   # 产物 apps/web/dist
```
后端地址与密钥被打进 JS bundle，运行时前端据此直连后端并带 `X-API-Key`。

### 1.3 部署
| 环境 | 方式 | 目标 |
|---|---|---|
| 生产 | 手动 `wrangler pages deploy dist --project-name mistake --branch main` | `app.toton123.xyz` |
| PR 预览 | GitHub Actions 自动（`deploy.sh` 里 `wrangler ... --branch <slug>`） | `<slug>.mistake-381.pages.dev` |

- Cloudflare Pages 项目 `mistake`（默认域 `mistake-381.pages.dev`）。
- DNS 在 Cloudflare：`app` 橙云（Pages 托管）；`api`/`*.api` 灰云 CNAME → ALB。
- `<slug>` = 分支名小写、非字母数字转 `-`、截断 28 字符（见 `deploy/aws/pr-preview/deploy.sh`）。

---

## 2. 后端 Go 链路：开发 → 部署

代码：`apps/server-go`（Go + chi + pgx + sqlc + PostgreSQL）。图片存储抽象 `internal/storage`（`STORAGE=local|s3`）；AI 走 DashScope 通义千问（`DASHSCOPE_API_KEY` 未配置时 `/api/recognize`、`/api/similar` 返回 503）。

### 2.1 本地开发（工具链在 `/opt/homebrew/bin`）
```bash
cd apps/server-go
export DATABASE_URL="postgres://zhaoyu@localhost:5432/mistake?sslmode=disable"
migrate -path migrations -database "$DATABASE_URL" up   # 建表（首次）
# 改了 queries/ 或 migrations/ 后：sqlc generate
lsof -ti:3000 | xargs kill 2>/dev/null; go run .        # :3000
```
Postgres 以 brew service 运行，库名 `mistake`。改 env/key 必须重启才生效。

### 2.2 配置（全靠环境变量，无硬编码）
`DATABASE_URL` · `CORS_ORIGIN`(逗号分隔白名单) · `API_KEY`(空=本地不校验) · `STORAGE` · `UPLOAD_DIR`/`PUBLIC_BASE_URL`(local) · `S3_BUCKET`/`AWS_REGION`/`S3_PUBLIC_BASE_URL`(s3) · `PORT`。见 `apps/server-go/.env.example`。

**迁移随启动自动跑**：`migrate.go` 用 `go:embed migrations/*.sql` + golang-migrate，启动 `Up()`（幂等）+ seed dev-user。**注意：只建表不建库**——目标 database 必须先存在。这正是 PR 预览要先 `CREATE DATABASE` 的原因。

`GET /health` 不走鉴权（ALB 健康检查）；`/api/*` 过 `X-API-Key` 中间件；CORS 白名单允许头含 `X-API-Key`。

### 2.3 镜像（`apps/server-go/Dockerfile`）
多阶段构建，run 阶段用 `scratch`+CA 证书。`GO_IMAGE` 可覆盖基础镜像。**架构必须与任务定义 `runtimePlatform` 一致**：
- 生产 = **arm64**（`docker build --platform linux/arm64`，本机 M 芯片原生）
- PR 预览 = **x86**（GHA `buildx --platform linux/amd64`，托管 runner 原生、快）

### 2.4 部署
| 环境 | 方式 | 说明 |
|---|---|---|
| 生产 | 手动（RUNBOOK 附录 B）：`docker build/push` ECR(arm64) → `ecs update-service --force-new-deployment` | 任务定义 family `mistake` |
| PR 预览 | GitHub Actions 自动：`buildx` amd64 推 ECR `pr-N` → `deploy.sh` 注册任务定义/TG/ALB 规则/ECS 服务 | 见第 3 节 |

ECS 任务启动流程（两环境相同）：从 **ECR** 拉镜像 → 从 **SSM** 取 secret（`DATABASE_URL`/`API_KEY`/`DASHSCOPE_API_KEY`）→ 连 **RDS** 自动跑迁移+seed → `/health` 变 healthy → ALB 挂上流量。图片读写走 **S3**（Task Role 授权）。

---

## 3. PR 全过程

自动化编排器 = **GitHub Actions**（`.github/workflows/pr-preview.yml`），因本账号 CodeBuild 并发被封为 0 而选用。CI 用 **OIDC** 假设 IAM 角色 `mistake-gha-role`（无长期密钥）。私有 RDS 的建/删库交给 **VPC Lambda** `mistake-pr-db`。

### 3.1 命名约定（全部由 PR 号 N / 分支 slug 派生）
| 资源 | 值 |
|---|---|
| 后端域名 | `pr-<N>.api.toton123.xyz` |
| 前端预览 | `<slug>.mistake-381.pages.dev` |
| 镜像 | `mistake-server:pr-<N>` |
| ECS 服务 / 任务定义 | `mistake-pr-<N>` |
| Target Group | `mistake-pr-<N>-tg` |
| ALB 监听规则 | Host=`pr-<N>.api.toton123.xyz` → 上面 TG |
| 逻辑库 | `mistake_pr_<N>`（共享实例 `mistake-db`） |
| per-PR 密钥 | SSM `/mistake/pr/<N>/DATABASE_URL` |

两个 URL 都能由 N/slug 提前算出 → 后端启动前就知道前端 origin（填 `CORS_ORIGIN`），前端构建前就知道后端 URL（填 `VITE_SERVER_URL`），无先后依赖。

### 3.2 开 / 更新 PR（`opened` / `synchronize` / `reopened`）→ deploy job
1. `checkout` → `configure-aws-credentials`（OIDC 假设 `mistake-gha-role`）→ `amazon-ecr-login`。
2. `buildx --platform linux/amd64` 构建 Go 镜像，打 tag `pr-<N>` 推 ECR（~40 秒）。
3. `setup-node@22`（wrangler@4 要求 Node≥22）。
4. `bash deploy/aws/pr-preview/deploy.sh`（`DB_VIA_LAMBDA=1`）：
   1. 调 Lambda `mistake-pr-db` `{action:create}` → 在共享 RDS 建库 `mistake_pr_<N>`（幂等）。
   2. 把 per-PR `DATABASE_URL` 写入 SSM `/mistake/pr/<N>/DATABASE_URL`（SecureString）。
   3. 由 `taskdef.tpl.json` 渲染并注册任务定义 `mistake-pr-<N>`（x86，注入 `CORS_ORIGIN`=前端预览 URL）。
   4. 建 Target Group + ALB 443 监听器上加 Host=`pr-<N>.api` 规则（优先级取空位）。
   5. 建/更新 ECS 服务 `mistake-pr-<N>`，等目标健康。
   6. `VITE_SERVER_URL=https://pr-<N>.api... VITE_API_KEY=<共享密钥> npm run build` → `wrangler pages deploy --branch <slug>`。
5. 产出：后端 `pr-<N>.api.toton123.xyz` + 前端 `<slug>.mistake-381.pages.dev`。

再推 commit = 又一次 `synchronize`，`deploy.sh` 幂等重跑（更新镜像、`force-new-deployment`、重新部署前端）。

### 3.3 关闭 / 合并 PR（`closed`）→ teardown job
`bash deploy/aws/pr-preview/teardown.sh`，反向删净（每步容忍已不存在）：ECS 服务 → ALB 监听规则 → Target Group → 反注册任务定义 → per-PR SSM 参数 → 调 Lambda `{action:drop}` 删库 `mistake_pr_<N>` → 删 ECR 镜像 `pr-<N>`。Cloudflare Pages 预览部署保留（便宜，可手动删）。

### 3.4 隔离 vs 共享
| 每 PR 独立 | 全局共享 |
|---|---|
| ECS 服务 / 任务定义 / TG / ALB 规则 | ECS 集群 `mistake` · ALB `mistake-alb` |
| 逻辑库 `mistake_pr_<N>` | RDS 实例 `mistake-db` · ECR 仓库 |
| 镜像 tag `pr-<N>` · SSM `/mistake/pr/<N>/*` | S3 桶 · `API_KEY`/`DASHSCOPE_API_KEY` · DashScope |
| 前后端预览 URL | 通配证书 · 通配 DNS · IAM 角色 · Lambda |

---

## 4. Cloud Map 服务发现（ECS → Lambda）

演示「ECS 服务经 AWS Cloud Map 发现并调用 Lambda」，与主业务链路解耦、随时可 curl 验证。因 Lambda 无固定 IP，采用 **HTTP 命名空间 + DiscoverInstances**（API 发现，非 DNS A 记录）。

```
ECS 后端 (Go, 用 mistake-task-role)
  │  GET /api/cloudmap-hello
  ├─ servicediscovery:DiscoverInstances(namespace=mistake-services, service=hello)
  │     → 实例属性 functionName = mistake-cloudmap-hello
  └─ lambda:Invoke(functionName) → 返回 Lambda 的问候 JSON
```

组成：
- **Cloud Map**：HTTP 命名空间 `mistake-services`；服务 `hello`；实例 `hello-1`（属性 `functionName` / `region`）。
- **Lambda** `mistake-cloudmap-hello`（Go, arm64, 执行角色 `mistake-cloudmap-lambda-role`）：普通调用型接口，返回 `{message, service, time}`。源码 `deploy/aws/cloudmap-demo/lambda-hello/`。
- **后端端点** `GET /api/cloudmap-hello`（`apps/server-go/internal/handlers/cloudmap.go`）：发现 → invoke，Lambda 地址不硬编码；命名空间/服务名可用 env `CLOUDMAP_NAMESPACE` / `CLOUDMAP_SERVICE` 覆盖（默认 `mistake-services` / `hello`）。
- **权限**：ECS 任务角色 `mistake-task-role` 加内联策略 `mistake-cloudmap`（`servicediscovery:DiscoverInstances` + `lambda:InvokeFunction`）。

> 为什么不走 Function URL + DNS：本账号护栏把 Lambda Function URL 的公网访问 403，且 Lambda 无 IP 不适合 Cloud Map 的 DNS A 记录；改用 HTTP 命名空间 API 发现 + `lambda:Invoke`，更贴近生产且无需公网暴露。

验证：`curl -H "X-API-Key: <key>" https://api.toton123.xyz/api/cloudmap-hello` → 返回含 `discoveredFunction` 与 `lambdaResponse`。

## 5. 关键设计决策

- **OIDC 免密钥**：GHA 不存 AWS 长期密钥，`mistake-gha-role` 信任限定 `repo:Tearl/mistake:*`。
- **RDS 私有 → VPC Lambda 建库**：托管 runner 在 VPC 外连不到私有 RDS，唯一需进 VPC 的「建/删库」由 `mistake-pr-db` 承担；其余（ECR/ECS/ELB/wrangler）都是公网 API。
- **每 PR 独立逻辑库**：复用同一 RDS 实例，成本近零，数据互不干扰；建库用的 master 连接串复用 `/mistake/DATABASE_URL`（连的就是 `postgres` 库，主用户有 CREATEDB）。
- **预览 x86 / 生产 arm64**：x86 在 amd64 runner 上原生构建，从 arm64+QEMU 的 ~7 分钟降到 ~40 秒；两者任务定义各自独立，互不影响。
- **脚本双模式**：`deploy.sh`/`teardown.sh` 的 `DB_VIA_LAMBDA` 开关，置 1 走 Lambda（GHA）、不设走 psql（CodeBuild），CodeBuild 解封可无缝回滚。

### 真跑暴露并已修的坑
- ECS 执行角色 `mistake-ecs-exec-role` 需授权 `ssm:GetParameters` 到 `/mistake/pr/*`（原只授权固定 3 个参数）。
- GHA `setup-node` 必须 22（wrangler@4 要求）。
- CodeBuild 新账号并发被封为 0（`AccountLimitExceededException`），故从 CodeBuild 转向 GHA。

---

## 6. 资源清单（us-east-1 · 账号 496251221975）

| 类别 | 资源 |
|---|---|
| 前端 | Cloudflare Pages `mistake`（`mistake-381.pages.dev`）；域 `app.toton123.xyz` |
| 后端入口 | ALB `mistake-alb`；443 监听器 `.../ee90628b7c1563fa`；证书 `api.toton123.xyz` + 通配 `*.api.toton123.xyz`；域 `api.toton123.xyz` |
| 计算 | ECS 集群 `mistake`；生产服务/任务定义 family `mistake`(arm64)；预览 `mistake-pr-<N>`(x86) |
| 镜像 | ECR `mistake-server` |
| 数据 | RDS `mistake-db`(连 `postgres` 库；SG `sg-083ab0e47ea42b8fd`)；S3 `mistake-uploads-496251221975` |
| 密钥 | SSM `/mistake/{DATABASE_URL,API_KEY,DASHSCOPE_API_KEY}` + `/mistake/pr/<N>/DATABASE_URL` |
| IAM | 执行 `mistake-ecs-exec-role`、任务 `mistake-task-role`、GHA `mistake-gha-role`、Lambda `mistake-pr-db-lambda-role`；OIDC provider `token.actions.githubusercontent.com` |
| PR 编排 | GHA `.github/workflows/pr-preview.yml`；VPC Lambda `mistake-pr-db`（arm64，SG `sg-034dd7da68ec36c3d`）；CodeBuild `mistake-pr-{deploy,teardown}`(备用，webhook 已删) |
| 网络 | 默认 VPC `vpc-0c36b43361100250a`；公有子网 `subnet-051776f835b1b44da / 0b62c3c3da2bf7fbd / 0f3f95ddb4ab8d5c5`；任务 SG `sg-0b23ad3efd38c816a` |
| Cloud Map | HTTP 命名空间 `mistake-services`(ns-jfii6h5th23drwwi) · 服务 `hello` · 实例 `hello-1`；演示 Lambda `mistake-cloudmap-hello`(角色 `mistake-cloudmap-lambda-role`)；任务角色策略 `mistake-cloudmap` |
| 外部 | 阿里云 DashScope（通义千问）；CF account `f350f1151c3d19066f82fd0c42e8ecd2` |

---

## 7. 运维

- **重新部署生产**：见 [RUNBOOK 附录 B](deploy/aws/RUNBOOK.md)（后端 build+push+`update-service`；前端 `wrangler ... --branch main`）。
- **RDS 改密码后**：更新 SSM `/mistake/DATABASE_URL`，并同步 Lambda env（否则新 PR 建库失败）：
  ```bash
  MASTER_URL="$(aws ssm get-parameter --name /mistake/DATABASE_URL --with-decryption --query Parameter.Value --output text)"
  aws lambda update-function-configuration --function-name mistake-pr-db --environment "Variables={MASTER_URL=$MASTER_URL}"
  ```
- **临时停生产省钱**：`ecs update-service --service mistake --desired-count 0`；RDS 可临时 Stop（最多 7 天）。
- **回滚到 CodeBuild**（若解封）：`deploy.sh` 不设 `DB_VIA_LAMBDA` 即走 psql；重挂 CodeBuild webhook、停用 GHA workflow，二者别同时开。
