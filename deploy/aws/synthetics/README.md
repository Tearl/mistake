# AWS API 巡检

这套 CloudFormation 资源每 5 分钟从 AWS 公网执行一次 API 巡检：

1. EventBridge Scheduler 定时调用 512 MB Lambda。
2. Lambda 请求 `GET /health`，要求 HTTP 2xx 且响应体为 `ok`。
3. Lambda 从 SSM SecureString 读取 `X-API-Key`，请求 `GET /api/stats`，要求 HTTP 2xx，并检查 `total`、`weekly` 和 `bySubject` 字段。
4. Lambda 向 `Mistake/Monitoring` 发布 `CheckSuccess=1|0`，同时将不含密钥和响应正文的诊断信息写入 CloudWatch Logs。
5. 连续两次失败或连续两个周期没有指标时，CloudWatch Alarm 进入 `ALARM`。可选的 SNS 邮件通知会发送故障和恢复消息。

目录沿用 `deploy/aws/synthetics` 和 stack name `mistake-synthetics`，但模板不再创建 CloudWatch Synthetics Canary、Canary S3 产物桶或 Synthetics runtime。

## 部署

前提：AWS CLI 已登录到生产账号；执行者有 CloudFormation、Lambda、EventBridge Scheduler、IAM、CloudWatch Logs、CloudWatch、SNS 和读取对应 SSM 参数的权限。

如果同名栈处于 `ROLLBACK_COMPLETE`，需要先删除失败栈：

```bash
aws cloudformation delete-stack \
  --region us-east-1 \
  --stack-name mistake-synthetics

aws cloudformation wait stack-delete-complete \
  --region us-east-1 \
  --stack-name mistake-synthetics
```

部署：

```bash
MSYS_NO_PATHCONV=1 aws cloudformation deploy \
  --region us-east-1 \
  --stack-name mistake-synthetics \
  --template-file deploy/aws/synthetics/template.yaml \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides \
    TargetBaseUrl=https://api.toton123.xyz \
    ApiKeyParameterName=/mistake/API_KEY \
    NotificationEmail=you@example.com
```

如果不需要邮件通知，删除 `NotificationEmail=...`。如果只巡检公开的 `/health`，传入空值：

```bash
ApiKeyParameterName=""
```

`MSYS_NO_PATHCONV=1` 对 Linux/macOS Bash 没有副作用，在 Windows Git Bash 中则是必需的。否则 Git Bash 会把 `/mistake/API_KEY` 自动转换成类似 `C:/Program Files/Git/mistake/API_KEY` 的 Windows 路径。仅加引号不能阻止该转换。

如果 SecureString 使用客户管理的 KMS Key，还需要传入其 ARN：

```bash
ApiKeyKmsKeyArn=arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000
```

使用邮件通知时，部署后必须在收件箱确认一次 AWS SNS 订阅，否则不会收到告警。

## 验证

查看 Lambda、Scheduler 和 Alarm 配置：

```bash
aws lambda get-function-configuration \
  --region us-east-1 \
  --function-name mistake-prod-api

aws scheduler get-schedule \
  --region us-east-1 \
  --name mistake-prod-api-schedule

aws cloudwatch describe-alarms \
  --region us-east-1 \
  --alarm-names mistake-prod-api-failed
```

手动执行一轮检查：

```bash
aws lambda invoke \
  --region us-east-1 \
  --function-name mistake-prod-api \
  --cli-binary-format raw-in-base64-out \
  --payload '{}' \
  /tmp/mistake-api-check.json

cat /tmp/mistake-api-check.json
```

查看最近日志或持续跟踪：

```bash
MSYS_NO_PATHCONV=1 aws logs tail /aws/lambda/mistake-prod-api \
  --region us-east-1 \
  --since 30m

MSYS_NO_PATHCONV=1 aws logs tail /aws/lambda/mistake-prod-api \
  --region us-east-1 \
  --since 10m \
  --follow
```

正常日志包含：

```text
{"event":"step_passed","step":"health",...}
{"event":"step_passed","step":"stats",...}
{"event":"check_completed","success":true,...}
```

控制台入口：

- Lambda → Functions → `mistake-prod-api`
- EventBridge → Scheduler → `mistake-prod-api-schedule`
- CloudWatch → All metrics → `Mistake/Monitoring`
- CloudWatch → Alarms → `mistake-prod-api-failed`

## 参数与运维

| 参数 | 默认值 | 说明 |
|---|---|---|
| `CheckerName` | `mistake-prod-api` | Lambda 名称、Scheduler 名称前缀、指标维度和 Alarm 名称前缀 |
| `TargetBaseUrl` | `https://api.toton123.xyz` | 待巡检的 HTTPS origin |
| `ApiKeyParameterName` | `/mistake/API_KEY` | 空值会关闭 `/api/stats` 步骤 |
| `ApiKeyKmsKeyArn` | 空 | 仅在 SecureString 使用客户管理 KMS Key 时填写 |
| `ScheduleExpression` | `rate(5 minutes)` | EventBridge Scheduler rate/cron 表达式 |
| `ScheduleState` | `ENABLED` | 设置为 `DISABLED` 可暂停执行 |
| `AlarmPeriodSeconds` | `300` | 应与调度间隔保持一致 |
| `FunctionTimeoutSeconds` | `30` | Lambda 总超时时间；单个 HTTP 请求固定为 10 秒 |
| `LogRetentionDays` | `30` | Lambda 日志保留天数 |
| `NotificationEmail` | 空 | 可选 SNS 告警邮箱 |

暂停巡检时重新部署并覆盖 `ScheduleState`：

```bash
MSYS_NO_PATHCONV=1 aws cloudformation deploy \
  --region us-east-1 \
  --stack-name mistake-synthetics \
  --template-file deploy/aws/synthetics/template.yaml \
  --capabilities CAPABILITY_IAM \
  --parameter-overrides ScheduleState=DISABLED
```

恢复时将值改回 `ENABLED`。暂停后，因为 Alarm 使用 `TreatMissingData=breaching`，约两个周期后会进入 `ALARM`；计划内暂停时可以同时临时禁用 Alarm actions。

删除整套监控：

```bash
aws cloudformation delete-stack \
  --region us-east-1 \
  --stack-name mistake-synthetics
```

模板删除时会删除 Lambda 日志组。此前 Synthetics 模板可能留下带 `Retain` 策略的 S3 bucket，它已不受新栈管理，需要在确认无用后单独清理。

## 安全设计

- API Key 不进入 Git、CloudFormation 参数、Lambda 环境变量或日志；Lambda 运行时以最小权限从 SSM 解密读取。
- 日志只记录步骤、HTTP 状态、耗时和经过限制的错误消息，不记录请求头或响应正文。
- Lambda 只被授予对应 SSM 参数、可选 KMS Key、固定 CloudWatch namespace 和自身日志组的权限。
- Scheduler 使用单独 IAM Role，只能调用这一个 Lambda。
- Lambda 不加入 VPC，通过公网 HTTPS 域名验证真实用户入口，也不会产生 NAT Gateway 成本。

## AWS 文档

- [EventBridge Scheduler](https://docs.aws.amazon.com/scheduler/latest/UserGuide/what-is-scheduler.html)
- [Lambda Node.js handler](https://docs.aws.amazon.com/lambda/latest/dg/nodejs-handler.html)
- [CloudWatch 自定义指标](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/publishingMetrics.html)
- [CloudWatch Alarm 缺失数据处理](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/AlarmThatSendsEmail.html)
