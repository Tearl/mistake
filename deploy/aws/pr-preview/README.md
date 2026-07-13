# PR 分支独立预览环境（Cloudflare + CodeBuild + IAM）

每开一个 PR，自动拉起一套**独立的前后端预览**；PR 关闭/合并时自动销毁。评审点开真实 URL 即可验证，互不干扰。生产环境（`app/api.toton123.xyz`）见 [../RUNBOOK.md](../RUNBOOK.md)，本方案**复用**其大部分资源。

## 工作原理

```
GitHub PR opened/synchronize ──webhook──▶ CodeBuild: mistake-pr-deploy
                                             ├─ docker build → ECR:mistake-server:pr-<N>
                                             ├─ deploy.sh
                                             │    ├─ CREATE DATABASE mistake_pr_<N>（共享 RDS）
                                             │    ├─ register taskdef mistake-pr-<N>
                                             │    ├─ TargetGroup + ALB 监听规则(Host=pr-<N>.api.toton123.xyz)
                                             │    ├─ ECS 服务 mistake-pr-<N>（Fargate，共享集群/ALB）
                                             │    └─ 前端 build(VITE_SERVER_URL=后端URL) → wrangler pages deploy --branch <slug>
                                             └─ ✅ pr-<N>.api.toton123.xyz + <slug>.mistake-381.pages.dev

GitHub PR merged/closed ──webhook──▶ CodeBuild: mistake-pr-teardown → teardown.sh（反向全删）
```

三角色分工：**CodeBuild** 唯一编排（webhook+构建+部署+销毁）；**Cloudflare** 前端预览托管 + 通配 DNS + ACM 验证；**IAM** 一个最小权限 service role。

## 命名约定（全部由 PR 号 N / 分支 slug 派生）

| 资源 | 值 |
|---|---|
| 后端域名 | `pr-<N>.api.toton123.xyz` |
| 前端预览 | `<slug>.mistake-381.pages.dev` |
| 镜像 tag | `mistake-server:pr-<N>` |
| ECS 服务 / 任务定义 | `mistake-pr-<N>` |
| Target Group | `mistake-pr-<N>-tg` |
| 逻辑库 | `mistake_pr_<N>`（共享实例 `mistake-db`） |
| per-PR DB 密钥 | SSM `/mistake/pr/<N>/DATABASE_URL` |

> `<slug>` = 分支名小写、非字母数字转 `-`、截断 28 字符（见 `deploy.sh`，与 Cloudflare Pages 分支别名规则一致）。

---

## 一次性基建（只做一次）

以下用生产账号真值：`ACCOUNT=496251221975`、`REGION=us-east-1`、ALB=`mistake-alb`、RDS SG=`sg-083ab0e47ea42b8fd`、任务 SG=`sg-0b23ad3efd38c816a`、默认 VPC=`vpc-0c36b43361100250a`、公有子网 `subnet-051776f835b1b44da subnet-0b62c3c3da2bf7fbd subnet-0f3f95ddb4ab8d5c5`。

```bash
export ACCOUNT=496251221975 REGION=us-east-1
export VPC_ID=vpc-0c36b43361100250a
export SUBNETS="subnet-051776f835b1b44da,subnet-0b62c3c3da2bf7fbd,subnet-0f3f95ddb4ab8d5c5"
export TASK_SG=sg-0b23ad3efd38c816a RDS_SG=sg-083ab0e47ea42b8fd
```

### 1. ACM 通配证书 `*.api.toton123.xyz`
```bash
aws acm request-certificate --domain-name '*.api.toton123.xyz' \
  --validation-method DNS --region $REGION            # 记 WILDCARD_CERT_ARN
aws acm describe-certificate --certificate-arn WILDCARD_CERT_ARN \
  --query "Certificate.DomainValidationOptions[0].ResourceRecord"
```
把返回的 CNAME 加到 Cloudflare（灰云），等状态 `ISSUED`。

### 2. 把通配证书加到 ALB 443 监听器（作为额外证书，保留现有 api 证书）
```bash
ALB_ARN=$(aws elbv2 describe-load-balancers --names mistake-alb --query 'LoadBalancers[0].LoadBalancerArn' --output text)
export ALB_LISTENER_ARN=$(aws elbv2 describe-listeners --load-balancer-arn $ALB_ARN \
  --query "Listeners[?Port==\`443\`].ListenerArn | [0]" --output text)
aws elbv2 add-listener-certificates --listener-arn $ALB_LISTENER_ARN \
  --certificates CertificateArn=WILDCARD_CERT_ARN
```

### 3. Cloudflare 通配 DNS
Cloudflare → toton123.xyz → DNS：加 `CNAME  *.api  →  mistake-alb-665430821.us-east-1.elb.amazonaws.com`，**灰云（DNS only）**。免费版通配代理不可用，灰云即可（后端本就走 ALB 的 HTTPS）。

### 4. CodeBuild → RDS 网络放行
CodeBuild 会放进默认 VPC 做 `CREATE DATABASE`。给它建 SG 并在 RDS SG 放行：
```bash
export CB_SG=$(aws ec2 create-security-group --group-name mistake-codebuild-sg \
  --description "codebuild pr-preview" --vpc-id $VPC_ID --query GroupId --output text)
aws ec2 authorize-security-group-ingress --group-id $RDS_SG \
  --protocol tcp --port 5432 --source-group $CB_SG
```

### 5. 新增 SSM 密钥（只需一个）
```bash
# Cloudflare Pages 部署 token（权限 Account > Cloudflare Pages > Edit）
aws ssm put-parameter --name /mistake/CF_API_TOKEN --type SecureString --value "cf-xxxx"
```
> 建库/删库用的 master 连接串**复用现有 `/mistake/DATABASE_URL`**（它连的就是共享实例的 `postgres` 库，主用户有 CREATEDB 权限；脚本把末段库名替换成 `mistake_pr_<N>`）。同样复用 `/mistake/API_KEY`、`/mistake/DASHSCOPE_API_KEY`。

### 6. IAM CodeBuild role
```bash
cat > /tmp/trust.json <<'JSON'
{ "Version":"2012-10-17","Statement":[{"Effect":"Allow",
  "Principal":{"Service":"codebuild.amazonaws.com"},"Action":"sts:AssumeRole"}] }
JSON
aws iam create-role --role-name mistake-codebuild-role \
  --assume-role-policy-document file:///tmp/trust.json
aws iam put-role-policy --role-name mistake-codebuild-role \
  --policy-name mistake-pr-preview \
  --policy-document file://deploy/aws/pr-preview/iam-codebuild-policy.json
```

### 7. 两个 CodeBuild 项目
控制台最省事（源=GitHub、勾选 Webhook、事件过滤器）。要点：
- **源**：GitHub 连本仓库（首次要在 CodeBuild 授权 GitHub OAuth/App）。
- **环境**：Amazon Linux `aarch64-standard`；**开启 Privileged**（deploy 项目要 docker build）；服务角色 `mistake-codebuild-role`；**VPC** 选默认 VPC + 上面 3 个子网 + SG `mistake-codebuild-sg`。
- **项目级环境变量**（明文即可，非密钥）：
  ```
  ACCOUNT=496251221975  REGION=us-east-1  CLUSTER=mistake
  ALB_LISTENER_ARN=<第2步的>  VPC_ID=vpc-0c36b43361100250a
  SUBNETS=subnet-...,subnet-...,subnet-...   TASK_SG=sg-0b23ad3efd38c816a
  BASE_DOMAIN=api.toton123.xyz  PAGES_PROJECT=mistake  PAGES_DOMAIN=mistake-381.pages.dev
  CLOUDFLARE_ACCOUNT_ID=f350f1151c3d19066f82fd0c42e8ecd2   # 账号下多 Pages 项目时必需
  GO_IMAGE=m.daocloud.io/docker.io/library/golang:1.26
  ```
- **mistake-pr-deploy**：Buildspec = `deploy/aws/pr-preview/buildspec-deploy.yml`；Webhook 事件过滤 `PULL_REQUEST_CREATED, PULL_REQUEST_UPDATED, PULL_REQUEST_REOPENED`。
- **mistake-pr-teardown**：Buildspec = `deploy/aws/pr-preview/buildspec-teardown.yml`；Webhook 事件过滤 `PULL_REQUEST_MERGED, PULL_REQUEST_CLOSED`；无需 Privileged。

CLI 版命令见文末附录。

---

## 验证（开一个测试 PR 走通）

1. 建分支改点小东西，提 PR → CodeBuild `mistake-pr-deploy` 应被触发、构建绿。
2. 后端：
   ```bash
   curl https://pr-<N>.api.toton123.xyz/health                          # ok
   curl -H "X-API-Key: <API_KEY>" https://pr-<N>.api.toton123.xyz/api/stats  # 200 JSON
   curl https://pr-<N>.api.toton123.xyz/api/stats                       # 401
   ```
3. 前端：浏览器开 `https://<slug>.mistake-381.pages.dev`，过首页/上传(真识别)/统计；确认请求打到 `pr-<N>.api...`、带 `X-API-Key`、无 CORS 报错。
4. 数据隔离：`psql "$RDS_MASTER_URL" -c "\l" | grep mistake_pr_<N>`；进该库查行数确认数据落在这里。
5. 日志：`aws logs tail /ecs/mistake --since 10m | grep pr-<N>`，能看到启动自动跑迁移+seed。
6. 销毁：合并/关闭 PR → `mistake-pr-teardown` 触发 → 确认服务/TG/监听规则/`mistake_pr_<N>` 库/ECR tag 全没了，`curl` 后端域名 404/503。

## 排查
- **构建卡在 docker build**：确认 deploy 项目开了 Privileged。
- **`CREATE DATABASE` 超时**：CodeBuild 没进 VPC 或 RDS SG 没放行 `mistake-codebuild-sg`。
- **502/target unhealthy**：看 `/ecs/mistake` 日志流 `pr-<N>/...`；多半是 DATABASE_URL 连不上或迁移失败。
- **前端 CORS 报错**：`CORS_ORIGIN` 与实际预览域名不一致（分支名 slug 化后可能被截断），核对 taskdef env 与浏览器 origin。

## 已知取舍
- 每个开着的 PR = 1 个常驻 Fargate 任务（256/512，小额计费），靠 close 自动回收，不做 scale-to-zero。
- S3 复用同一桶同一 `uploads/` 前缀（Go 里写死），预览上传混入生产桶——公开读、低风险。要隔离后续给 `internal/storage` 加 `S3_PREFIX`。
- API_KEY 复用生产共享值；预览属内部用途。
- ALB 每监听器默认 100 条规则（够几十个并发 PR，可提额）。

---

## 附录：CodeBuild 项目 CLI（替代第 7 步控制台）

项目定义已落在 `codebuild-project-deploy.json` / `codebuild-project-teardown.json`（真值全填好；`SUBNETS` 含逗号，故用 JSON 而非 shorthand）。

```bash
# 前置：账号里要先有 GitHub 凭据（二选一）
#  A) PAT：read -rs GH_PAT; aws codebuild import-source-credentials \
#        --server-type GITHUB --auth-type PERSONAL_ACCESS_TOKEN --token "$GH_PAT"; unset GH_PAT
#  B) OAuth：控制台建项目时点 Connect to GitHub 授权一次
aws codebuild list-source-credentials    # 确认非空

aws codebuild create-project --cli-input-json file://deploy/aws/pr-preview/codebuild-project-deploy.json
aws codebuild create-project --cli-input-json file://deploy/aws/pr-preview/codebuild-project-teardown.json
aws codebuild create-webhook --project-name mistake-pr-deploy \
  --filter-groups '[[{"type":"EVENT","pattern":"PULL_REQUEST_CREATED,PULL_REQUEST_UPDATED,PULL_REQUEST_REOPENED"}]]'
aws codebuild create-webhook --project-name mistake-pr-teardown \
  --filter-groups '[[{"type":"EVENT","pattern":"PULL_REQUEST_MERGED,PULL_REQUEST_CLOSED"}]]'
```
