# SNS/SQS 异步识别与 ECS API Canary 上线手册

本文把本次改造分成“已完成的代码”和“需要在 AWS 执行的配置”。命令示例使用 AWS CLI v2，所有 `<>` 都必须替换成真实值；先在测试环境演练，再操作生产。

## 1. 代码改造范围

- API 新增 `POST /api/recognitions`，创建识别任务并向 SNS 发布 `recognition.requested` 事件；`requestId` 具有幂等约束。
- API 新增 `GET /api/recognitions/{jobId}`，返回 `queued / processing / retrying / succeeded / failed / dead_lettered / publish_failed`。
- Worker 以 SQS 长轮询消费事件，失败时不删除消息，由 SQS 重试；达到 `maxReceiveCount=3` 后进入 DLQ。
- 数据库新增 `recognition_jobs`，保存任务状态、尝试次数、结果和错误。
- Web 上传后改为创建任务并每 2 秒轮询；本地环境默认同步执行，便于不依赖 AWS 调试。
- 服务新增 `/health/live`、`/health/ready`、`/version`；迁移改为独立 `APP_MODE=migrate` 任务，避免 Canary 新旧任务并发执行迁移。

兼容说明：原来的 `POST /api/recognize` 暂时保留，可在新链路稳定后再下线。

## 2. 上线前参数

```powershell
$AwsRegion = "us-east-1"
$AccountId = aws sts get-caller-identity --query Account --output text
$Cluster = "mistake"
$ApiService = "mistake"
$WorkerService = "mistake-worker"
$UploadsBucket = "mistake-uploads-$AccountId"
$Image = "$AccountId.dkr.ecr.$AwsRegion.amazonaws.com/mistake-server:<git-sha>"
$AppVersion = "<git-sha>"
```

容器镜像必须使用不可变 SHA 标签，并保持 ARM64 构建，与任务定义的 `runtimePlatform` 一致。

## 3. 部署 SNS、SQS 与 DLQ

1. 如果需要告警通知，先创建或复用一个专门用于运维通知的 SNS Topic，并订阅邮箱/Chatbot；没有时把 `AlarmTopicArn` 留空。
2. 部署模板：

```powershell
aws cloudformation deploy `
  --region $AwsRegion `
  --stack-name mistake-messaging `
  --template-file deploy/aws/messaging/template.yaml `
  --capabilities CAPABILITY_NAMED_IAM `
  --parameter-overrides `
    AppName=mistake `
    ApiTaskRoleName=mistake-task-role `
    UploadsBucketName=$UploadsBucket `
    AlarmTopicArn="<可选告警Topic ARN>"
```

3. 记录输出：

```powershell
aws cloudformation describe-stacks `
  --region $AwsRegion `
  --stack-name mistake-messaging `
  --query "Stacks[0].Outputs[*].[OutputKey,OutputValue]" `
  --output table
```

4. 在控制台确认：SNS Subscription 为 `Confirmed`；SQS 主队列的 DLQ 和 `maxReceiveCount=3` 正确；主队列策略只允许该 Topic 的 `sqs:SendMessage`。

模板已经创建：

- 事件 Topic、识别主队列、14 天保留的 DLQ；
- SNS 原始消息投递和 `eventType=recognition.requested` 过滤；
- API 的 `sns:Publish` 权限；
- Worker Task Role（SQS 消费 + 读取 `uploads/*`）；
- DLQ 非空和积压超过 5 分钟告警。

## 4. 注册任务定义并先执行迁移

复制 `deploy/aws/prod/*.tpl.json` 为临时文件，把以下占位符替换后再注册，不要直接改模板：

- `__ACCOUNT__`、`__REGION__`、`__IMAGE__`；
- `__SNS_TOPIC_ARN__`、`__SQS_QUEUE_URL__`、`__WORKER_TASK_ROLE_ARN__`；
- `__APP_VERSION__`、`__BUILD_TIME__`。

```powershell
aws ecs register-task-definition --region $AwsRegion --cli-input-json file://taskdef-migrate.json
aws ecs register-task-definition --region $AwsRegion --cli-input-json file://taskdef-worker.json
aws ecs register-task-definition --region $AwsRegion --cli-input-json file://taskdef-api.json
```

先运行一次迁移任务。当前生产使用默认 VPC 的公有子网，迁移任务复用 API 的网络配置：

```powershell
$MigrationTaskArn = aws ecs run-task `
  --region $AwsRegion `
  --cluster $Cluster `
  --launch-type FARGATE `
  --task-definition "mistake-migrate:<revision>" `
  --network-configuration "awsvpcConfiguration={subnets=[subnet-051776f835b1b44da,subnet-0b62c3c3da2bf7fbd,subnet-0f3f95ddb4ab8d5c5],securityGroups=[sg-0b23ad3efd38c816a],assignPublicIp=ENABLED}" `
  --query "tasks[0].taskArn" --output text

aws ecs wait tasks-stopped --region $AwsRegion --cluster $Cluster --tasks $MigrationTaskArn
aws ecs describe-tasks --region $AwsRegion --cluster $Cluster --tasks $MigrationTaskArn `
  --query "tasks[0].containers[0].[exitCode,reason]" --output table
```

只有 `exitCode=0` 才继续。生产 API/Worker 的 `AUTO_MIGRATE` 必须保持 `false`。

## 5. 创建或更新 Worker Service

Worker 没有端口和负载均衡器，建议初始 `desired-count=1`：

```powershell
aws ecs create-service `
  --region $AwsRegion `
  --cluster $Cluster `
  --service-name $WorkerService `
  --task-definition "mistake-worker:<revision>" `
  --desired-count 1 `
  --launch-type FARGATE `
  --deployment-configuration "minimumHealthyPercent=100,maximumPercent=200,deploymentCircuitBreaker={enable=true,rollback=true}" `
  --network-configuration "awsvpcConfiguration={subnets=[subnet-051776f835b1b44da,subnet-0b62c3c3da2bf7fbd,subnet-0f3f95ddb4ab8d5c5],securityGroups=[sg-0b23ad3efd38c816a],assignPublicIp=ENABLED}"
```

已存在时使用 `aws ecs update-service --force-new-deployment`。Worker Security Group 只需出站访问 RDS、S3、SQS、DashScope 和 CloudWatch Logs；不需要入站规则。当前项目沿用公有子网和 Public IP；若以后迁入私有子网，需配置 NAT/相应 VPC Endpoint，其中 DashScope 公网 API 仍需要公网出口。

## 6. 一次性配置 API Canary

### 6.1 创建备用 Target Group

在 EC2 控制台进入 **Target Groups → Create target group**：

1. Target type 选 `IP`，协议/端口选 `HTTP:3000`，VPC 与现有 API 一致。
2. 名称用 `mistake-api-canary`。
3. Health check path 使用 `/health/ready`，成功码 `200`，建议 interval 15 秒、healthy threshold 2、unhealthy threshold 3。
4. 不手工注册 Target，ECS 会管理注册。

### 6.2 创建显式 Listener Rules

ECS 原生 Canary 要求生产流量和测试流量都对应“规则 ARN”，不能只依赖 Listener 的默认动作。

1. 在现有 HTTPS Listener 上创建生产规则，例如 Host header 为 `api.toton123.xyz`，转发到当前主 Target Group。
2. 创建更高优先级的测试规则，同时匹配 Host header 和 HTTP header `X-Mistake-Canary=1`，先转发到备用 Target Group。
3. 记录两个 Rule ARN、两个 Target Group ARN。生产客户端绝不能发送该测试 Header。

### 6.3 部署 Canary 校验钩子与 ECS 角色

```powershell
aws cloudformation deploy `
  --region $AwsRegion `
  --stack-name mistake-canary `
  --template-file deploy/aws/canary/template.yaml `
  --capabilities CAPABILITY_NAMED_IAM `
  --parameter-overrides `
    AppName=mistake `
    TargetBaseUrl=https://api.toton123.xyz `
    ApiKeyParameterName=/mistake/API_KEY
```

该模板创建三个资源：ECS 负载均衡基础设施角色、ECS 调用 Lambda 的角色、校验 Lambda。Lambda 通过测试 Header 访问绿色版本，依次检查 readiness、版本号和带 API Key 的 stats 接口；任何一步失败都会返回 `FAILED` 并触发回滚。

部署后记录 `InfrastructureRoleArn`、`CanaryValidatorArn`、`HookInvokeRoleArn`。执行发布的 IAM 身份还必须拥有这两个 ECS 角色的 `iam:PassRole`，并限制 `iam:PassedToService=ecs.amazonaws.com`。

### 6.4 创建回滚告警

在 CloudWatch 创建下面两个告警，名称要与 `service-canary.tpl.json` 一致：

1. `mistake-api-target-5xx`：`AWS/ApplicationELB > HTTPCode_Target_5XX_Count`，维度选生产 ALB；1 分钟周期，连续 2 个周期总和 >= 5。
2. `mistake-api-latency-p95`：`AWS/ApplicationELB > TargetResponseTime`，同一 ALB；p95，1 分钟周期，连续 3 个周期 >= 3 秒。

两个告警都使用 `Treat missing data = not breaching`。低流量环境主要依赖 Lambda 校验；阈值上线前应根据现网基线调整，避免正常流量触发误回滚。

### 6.5 启用并发布 Canary

复制 `deploy/aws/prod/service-canary.tpl.json`，替换全部占位符，然后执行：

```powershell
aws ecs update-service --region $AwsRegion --cli-input-json file://service-canary.json
aws ecs wait services-stable --region $AwsRegion --cluster $Cluster --services $ApiService
```

当前模板策略为：先把 10% 生产流量切到绿色版本，观察 15 分钟，再切 100%，最后保留新旧版本 10 分钟。`POST_TEST_TRAFFIC_SHIFT` 钩子会在任何生产流量进入绿色版本前完成测试。

观察以下位置：ECS Service Deployments、Lambda `/aws/lambda/mistake-ecs-canary-validator` 日志、两个 Target Group 的 healthy host/5xx/latency、应用日志、SQS 队列深度与 DLQ。

## 7. 验收步骤

1. `GET /health/live` 返回 200；`GET /health/ready` 返回 200；`GET /version` 的 `X-App-Version` 等于发布 SHA。
2. 上传图片后 `POST /api/recognitions` 返回 202 和 `jobId`。
3. 数据库任务经历 `queued → processing → succeeded`，页面自动显示识别结果。
4. 临时使用错误 DashScope Key 验证重试；恢复后不得遗留消息。故意让消息连续失败 3 次，确认进入 DLQ 且告警触发。
5. 使用相同 `requestId` 重复请求，不产生第二条任务。
6. Canary 期间使用 `X-Mistake-Canary: 1` 请求 `/version`，必须命中新版本；普通请求仍按设定权重分流。

## 8. 回滚与 DLQ 处置

- Canary 的 Lambda 或 CloudWatch Alarm 失败会由 ECS 自动回滚。手工回滚时，把 API Service 更新为上一版 Task Definition，并保留同一 Canary 配置。
- 数据库迁移只增加表，不阻塞旧版本。回滚应用时不要执行 down migration；确认完全弃用异步链路后再单独评估删表。
- Worker 代码故障时先将 `mistake-worker` desired count 调为 0，避免继续消费；修复发布后恢复为 1。
- DLQ 重放前先修复根因并抽查消息。SQS 控制台进入 DLQ → **Start DLQ redrive** → 目标选原识别队列 → 先限速小批量；同时观察错误率和队列年龄。任务处理具备事件/请求幂等约束，重放不会创建第二个任务。
- SNS 发布故障会产生 `publish_failed` 状态，不会伪装为排队成功；修复后由运维脚本按任务 ID 重新发布（本次未提供自动重发，防止故障期间放大流量）。

## 9. 本地与测试环境

本地 `.env` 保持：

```dotenv
APP_MODE=api
AUTO_MIGRATE=true
ASYNC_RECOGNITION=false
```

这样新异步 API 会在当前进程内同步处理并返回相同任务结构，不要求开发者本机配置 SNS/SQS。集成环境应启用 `ASYNC_RECOGNITION=true`，并分别运行 `APP_MODE=api` 与 `APP_MODE=worker`。

## 10. 增量费用估算

以下是相对当前已有 API、ALB、RDS、S3 和网络的增量，按美东弗吉尼亚北部区、单个 ARM64 `0.25 vCPU / 0.5 GB` Worker、每月 730 小时估算。实际账单以 AWS Pricing Calculator 和 Cost Explorer 为准。

| 项目 | 低流量月增量 | 说明 |
| --- | ---: | --- |
| Worker Fargate | 约 USD 7–10 | `desired-count=1` 全天运行，是主要固定增量 |
| SNS + SQS + DLQ | 通常 USD 0 | SQS 每月约 12.96 万次空队列长轮询，加业务收发删；小于每月 100 万请求免费额度时通常不收费。SNS 低流量也通常在免费额度内 |
| Canary 额外 API Task | 通常低于 USD 0.20 | 只在每次发布的测试、流量观察和 bake 窗口同时保留新旧 Task；发布次数和 desired count 越多越高 |
| Lambda 校验钩子 | 通常 USD 0 | 每次发布调用少量次数，一般在 Lambda 免费额度内 |
| CloudWatch | 约 USD 0–1 | 4 个标准分辨率告警及少量日志；是否免费取决于账号已使用的免费额度 |
| 数据库与 S3 | 接近 USD 0 | 任务表只增加少量行；图片仍沿用现有桶 |

因此，在已有 ALB、数据库和网络的前提下，建议把本改造预算记为 **USD 8–12/月**，另加原有 DashScope 调用费。异步化本身不增加正常任务的 AI 调用次数，但失败重试最多可能把单个失败任务放大到 3 次调用。

最大的隐藏成本是网络：如果 Worker 所在私有子网当前没有公网出口，而为了访问 DashScope 新建 NAT Gateway，NAT 的小时费和流量费可能远高于消息组件与 Worker，通常会额外达到每月数十美元。当前 API 已调用 DashScope；优先让 Worker 复用现有安全出口，不要为它单独新建 NAT。若后续希望进一步省钱，可用 SQS 队列深度驱动 Worker 从 0 扩缩，但会引入冷启动与额外监控配置，本次默认采用稳定的常驻 1 个 Task。
