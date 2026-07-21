# 性能 SDK + ECS 清洗 + /ops 面板（perf）

对齐参考图「泳道 3 · 观测与数据链路」：应用内**性能 SDK** 每 5 秒批量把
{路由,方法,状态,延迟} 写到 CloudWatch Logs → **ECS 清洗任务** 定时聚合成
p50/p90/p99/错误率 → 写 S3 → 前端 **/ops 面板** 可视化。

## 组件

| 组件 | 位置 | 说明 |
| --- | --- | --- |
| 性能 SDK | `apps/server-go/internal/perf`（`Middleware`+`Collector`） | chi 中间件采集，后台每 5s 批量落地；生产 → CloudWatch Logs `/mistake/perf`，本地 → JSONL 文件 |
| 清洗任务 | 同一二进制 `APP_MODE=perfagg` | 读日志组近 N 分钟 → `Aggregate` → 写 `ops/aggregates/latest.json`（S3 或本地） |
| 后端接口 | `GET /api/ops/summary` | 读最新聚合返回前端 |
| 前端面板 | `apps/web/src/routes/ops.tsx`（统计页有入口） | KPI + 请求量时序 + 各路由延迟/错误 |

## AWS 服务

- 新建：CloudWatch Logs 组 `/mistake/perf`、`mistake-perfagg` 任务定义、`AWS::Scheduler` 定时、`mistake-perfagg-task-role` 与调度角色。
- 复用：ECS Fargate、S3（`ops/*` 前缀）、现有 API/worker 角色（补 `logs:PutLogEvents` + `s3:GetObject ops/*`）。
- 本地开发无需任何 AWS：SDK 落 `perf_logs/`，清洗落 `ops_data/`，`/ops` 照常渲染（已本地验证）。

## 本地验证（已跑通）

```bash
cd apps/server-go
STORAGE=local PERF_ENABLED=true APP_MODE=api DATABASE_URL=postgres://zhaoyu@localhost:5432/mistake?sslmode=disable go run .
# 打点几个接口后：
STORAGE=local APP_MODE=perfagg OPS_WINDOW_MINUTES=15 go run .   # 清洗一次
curl localhost:3000/api/ops/summary                             # 看聚合
# 前端 npm run dev:bare → 打开 /ops
```

## 部署（后续）

1. 后端已含 SDK：正常 build+push 镜像、更新 API/worker 服务即可开始产生 `/mistake/perf` 日志。
2. 注册清洗任务定义（占位符替换后）：
   ```bash
   aws ecs register-task-definition --cli-input-json file://taskdef-perfagg.json
   ```
3. 部署本栈（建日志组、角色、定时；给现有角色补权限）：
   ```bash
   aws cloudformation deploy --stack-name mistake-perf \
     --template-file deploy/aws/perf/template.yaml --capabilities CAPABILITY_NAMED_IAM \
     --parameter-overrides UploadsBucketName=mistake-uploads-<acct> \
       PerfAggTaskDefinition=<perfagg-taskdef-arn> \
       Subnets=<subnet-a>,<subnet-b>,<subnet-c> SecurityGroups=<sg>
   ```
4. `/api/ops/summary` 会在第一轮清洗后返回数据；`/ops` 面板每 30s 自动刷新。

## 说明

- 性能 SDK 对主流程零阻塞：只入内存缓冲，缓冲溢出丢弃，落地在后台协程。
- 错误定义：HTTP 状态码 ≥ 500 记为错误（可按需调整 `aggregate.go`）。
- 日志组保留默认 7 天，够清洗窗口用；聚合产物只留 `latest.json`（可加时间戳版本）。
