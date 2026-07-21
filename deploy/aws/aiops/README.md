# AI Ops Agent（mistake-aiops）

给 mistake 项目加一个**轻量 AI 运维大脑**：CloudWatch 告警或定时巡检触发一个 Lambda，
采集运维快照 → 调通义千问 `qwen-plus` 出结构化诊断 → 在 GitHub 建 Issue 当工单。

**只读诊断，绝不改生产。** 密钥运行时从 SSM SecureString 取，不进环境变量、不进代码。

## 能力对应（参考「企业级 AI Ops Agent」）

| 参考能力 | 本项目实现 |
| --- | --- |
| 异常分析 / 根因定位 | 采集告警+日志+队列+部署状态喂 LLM，输出 `summary`/`rootCause` |
| 性能优化建议 | LLM 基于 p95、队列深度给 `suggestions`（顾问性质） |
| 安全风险识别 | 扫 `/ecs/mistake` 错误日志，LLM 标记 `security` |
| 自动生成工单 | 在 `Tearl/mistake` 建带 `aiops` 标签的 Issue，按告警名去重 |
| 主动发现 | EventBridge 每 30 分钟巡检；健康则静默，异常才建单 |

## 架构与 AWS 服务

```
CloudWatch 告警 ──▶ SNS 告警主题 ──▶ Lambda mistake-aiops-agent
EventBridge 定时 ─────────────────▶       │  采集：DescribeAlarms · FilterLogEvents(/ecs/mistake)
                                          │        GetQueueAttributes(主队列+DLQ) · DescribeServices
                                          ▼
                                    通义千问 qwen-plus（外部）
                                          ▼
                                    GitHub Issue（外部，PAT 存 SSM）
```

- 复用：SNS、CloudWatch(Logs/Alarms)、SQS/DLQ、ECS、SSM、CloudFormation、EventBridge Scheduler。
- 新建：仅 1 个 Lambda + 1 个执行角色（最小只读权限）。
- 外部依赖：DashScope API、GitHub API。

## 前置：写入 GitHub PAT

建一个 **fine-grained PAT**，仅授予 `Tearl/mistake` 仓库的 `Issues: Read and write`，然后：

```bash
aws ssm put-parameter --name /mistake/GITHUB_TOKEN --type SecureString \
  --value 'github_pat_xxx' --region us-east-1
```

DashScope key 复用已有的 `/mistake/DASHSCOPE_API_KEY`，无需新建。

## 部署

```bash
export ALARM_TOPIC_ARN="arn:aws:sns:us-east-1:496251221975:mistake-synthetics-AlarmTopic-XXXX"
./deploy.sh
```

`deploy.sh` 会：`go build` arm64 → 打 `bootstrap` zip 传 S3 → `cloudformation deploy` 栈 `mistake-aiops`
（创建角色、Lambda、SNS 订阅、定时巡检、Agent 自身错误告警）。

参数默认值见 `template.yaml`：告警名清单、巡检频率 `rate(30 minutes)`、日志组 `/ecs/mistake`、
仓库 `Tearl/mistake`、SSM 参数名等，可按需覆盖。

## 验收

1. **主动巡检**：手动触发一次，健康时应静默返回。
   ```bash
   aws lambda invoke --function-name mistake-aiops-agent \
     --payload '{"source":"scheduled-scan"}' --cli-binary-format raw-in-base64-out /dev/stdout
   ```
2. **告警链路**：制造一次故障——例如把一条毒消息灌进 SQS 让它进 DLQ，触发
   `mistake-recognition-dlq-not-empty` → SNS → Agent → 应在仓库出现 `[AIOps][alarm:...]` Issue。
3. **去重**：同一告警持续 ALARM，不应反复建单（标题含 `[alarm:<name>]` 指纹，已存在则跳过）。
4. **最小权限**：确认角色只有只读 + `ssm:GetParameter`(两个参数)，无任何改生产权限。
5. Agent 自身报错 → `mistake-aiops-agent-errors` 告警 → 邮件。

## 回滚 / 关停

- 暂停主动巡检：把 `ScanSchedule` 的 `ScheduleState` 改 `DISABLED` 重部署，或控制台禁用。
- 完全移除：`aws cloudformation delete-stack --stack-name mistake-aiops`（不影响主服务与告警）。

## 边界说明

- 参考图那套 ELK/Prometheus/Grafana/ClickHouse/K8s 本项目没有；这里是**围绕 CloudWatch 的精简真实版**。
- Agent 只建议、只建单，不自动改生产。若未来要自动处置，应单独评估并加审批闸。
- `agent/go.mod` 的依赖版本为骨架初值，首次构建前跑一次 `cd agent && go mod tidy`。
