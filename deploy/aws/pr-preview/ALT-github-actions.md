# Plan B 改造手册：GitHub Actions + OIDC + VPC Lambda

> 当 CodeBuild 账号级并发封控（`AccountLimitExceededException: Cannot have more than 0 builds`）迟迟不解除时，用这套替代 CodeBuild 的**编排/构建**角色。**Cloudflare、ALB、ECS、RDS、S3、IAM 大部分、命名约定、`taskdef.tpl.json`、wrangler 部署逻辑全部照旧复用**，只换"谁来跑"和"建库怎么进 VPC"。

## 为什么需要 Lambda（核心约束）

RDS 是**私有**的（Public access No，在 VPC 内）。CodeBuild 能建库是因为它被放进了 VPC；GitHub Actions 的托管 runner 在 **VPC 外**，连不到 RDS。其余步骤（ECR 推镜像、注册任务定义、TG、ALB 规则、ECS 服务、wrangler）都是**公网 AWS API**，runner 能直接干。所以唯一要挪进 VPC 的是 **`CREATE/DROP DATABASE`**——用一个巴掌大的 VPC 内 Lambda 承担，GHA 用 `lambda invoke` 调它。

```
GitHub PR (opened/synchronize/reopened/closed)
  └─ GitHub Actions（VPC 外托管 runner）
       ├─ OIDC 假设 IAM 角色（无长期密钥）
       ├─ buildx 构建 linux/arm64 镜像 → 推 ECR:pr-<N>
       ├─ aws lambda invoke mistake-pr-db {action:create,pr:N}  ← 唯一进 VPC 的一步
       ├─ deploy.sh 的其余部分（taskdef/TG/ALB规则/ECS服务/等健康）
       └─ 前端 build(VITE_SERVER_URL) + wrangler pages deploy --branch <slug>
  关闭/合并 → teardown：lambda invoke {action:drop} + 删 ECS/TG/规则/镜像
```

## 与 CodeBuild 版的差异一览

| 关注点 | CodeBuild 版 | 本 Plan B |
|---|---|---|
| 触发+编排 | CodeBuild 项目 + webhook | `.github/workflows/pr-preview.yml` |
| 凭据 | CodeBuild service role | GitHub OIDC → IAM 角色（无密钥） |
| 建/删库 | `deploy.sh` 内 `psql`（CodeBuild 在 VPC 里） | VPC Lambda `mistake-pr-db`，GHA 调用 |
| arm64 构建 | 原生 arm CodeBuild | buildx+QEMU（慢几分钟）或改预览为 x86 |
| CF token | SSM | GHA Secret `CLOUDFLARE_API_TOKEN` |
| 复用 | — | `taskdef.tpl.json`、IAM 策略主体、`deploy.sh`/`teardown.sh` 逻辑、Cloudflare 全套 |

真值（us-east-1 / 账号 `496251221975`）：VPC `vpc-0c36b43361100250a`；公有子网 `subnet-051776f835b1b44da,subnet-0b62c3c3da2bf7fbd,subnet-0f3f95ddb4ab8d5c5`；任务 SG `sg-0b23ad3efd38c816a`；RDS SG `sg-083ab0e47ea42b8fd`；已放行 RDS:5432 的 `mistake-codebuild-sg=sg-034dd7da68ec36c3d`（Lambda 复用它即可直连 RDS）；ALB 443 监听器 `.../mistake-alb/a48a24d45f5a8de7/ee90628b7c1563fa`；ECR `mistake-server`；集群 `mistake`；CF account `f350f1151c3d19066f82fd0c42e8ecd2`；仓库 `Tearl/mistake`。

---

## 一、一次性基建

### 1. GitHub OIDC Provider（IAM，账号级只建一次）✅ 已存在
本账号已有：`arn:aws:iam::496251221975:oidc-provider/token.actions.githubusercontent.com`（建于 2026-06-29，clientID=`sts.amazonaws.com` 正确），**无需再建**。若换账号则：
```bash
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1
# 说明：AWS 现已用内置信任库校验 GitHub OIDC，thumbprint 仅为 API 必填项；上面是通用值。
```

### 2. GHA 用的 IAM 角色 ✅ 已建 `arn:aws:iam::496251221975:role/mistake-gha-role`
已挂内联策略 `mistake-pr-preview`(复用 iam-codebuild-policy.json) + `invoke-db-lambda`。信任策略（`gha-trust.json`）——**限定本仓库上下文**：
```json
{ "Version":"2012-10-17","Statement":[{
  "Effect":"Allow",
  "Principal":{"Federated":"arn:aws:iam::496251221975:oidc-provider/token.actions.githubusercontent.com"},
  "Action":"sts:AssumeRoleWithWebIdentity",
  "Condition":{
    "StringEquals":{"token.actions.githubusercontent.com:aud":"sts.amazonaws.com"},
    "StringLike":{"token.actions.githubusercontent.com:sub":"repo:Tearl/mistake:*"}
  }}]}
```
```bash
aws iam create-role --role-name mistake-gha-role \
  --assume-role-policy-document file://gha-trust.json
# 权限：直接复用现成策略文件，再加一条 lambda:InvokeFunction
aws iam put-role-policy --role-name mistake-gha-role --policy-name mistake-pr-preview \
  --policy-document file://deploy/aws/pr-preview/iam-codebuild-policy.json
aws iam put-role-policy --role-name mistake-gha-role --policy-name invoke-db-lambda \
  --policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"lambda:InvokeFunction","Resource":"arn:aws:lambda:us-east-1:496251221975:function:mistake-pr-db"}]}'
```
> `iam-codebuild-policy.json` 里的 `ec2:*NetworkInterface` 那条 GHA runner 用不到（无害，可留）；真正需要 ENI 的是下面 Lambda 的执行角色。

### 3. VPC 内 Lambda `mistake-pr-db`（建/删库）✅ 已部署并实测（create/幂等/drop 均通过）
`arn:aws:lambda:us-east-1:496251221975:function:mistake-pr-db`（provided.al2023, arm64, 128MB；执行角色 `mistake-pr-db-lambda-role` + `AWSLambdaVPCAccessExecutionRole`；VPC 三公有子网 + SG `sg-034dd7da68ec36c3d`；env `MASTER_URL` 取自 `/mistake/DATABASE_URL`）。源码 `deploy/aws/pr-preview/lambda-db/main.go`（`bootstrap`/`fn.zip` 已 gitignore）。重新部署：`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o bootstrap . && zip fn.zip bootstrap && aws lambda update-function-code --function-name mistake-pr-db --zip-file fileb://fn.zip`。源码如下：

```go
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	Action string `json:"action"` // "create" | "drop"
	PR     int    `json:"pr"`
}

var nameOK = regexp.MustCompile(`^[0-9]+$`)

func handle(ctx context.Context, e Event) (string, error) {
	if !nameOK.MatchString(fmt.Sprint(e.PR)) {
		return "", fmt.Errorf("bad pr: %v", e.PR)
	}
	db := fmt.Sprintf("mistake_pr_%d", e.PR)
	master := os.Getenv("MASTER_URL") // 连 postgres 库的 master 连接串（KMS 加密的 env）
	conn, err := pgx.Connect(ctx, master)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	switch e.Action {
	case "create":
		var exists bool
		_ = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", db).Scan(&exists)
		if !exists {
			if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
				return "", fmt.Errorf("create: %w", err)
			}
		}
		return "created " + db, nil
	case "drop":
		_, _ = conn.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()", db)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{db}.Sanitize()); err != nil {
			return "", fmt.Errorf("drop: %w", err)
		}
		return "dropped " + db, nil
	}
	return "", fmt.Errorf("unknown action %q", e.Action)
}

func main() { lambda.Start(handle) }
```
构建 + 部署（arm64 自定义运行时）：
```bash
cd deploy/aws/pr-preview/lambda-db
go mod init mistake-pr-db && go get github.com/aws/aws-lambda-go/lambda github.com/jackc/pgx/v5
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o bootstrap .
zip fn.zip bootstrap

# 执行角色：VPC 网卡 + （可选）不用 SSM，因为 master URL 走 env
aws iam create-role --role-name mistake-pr-db-lambda-role \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}'
aws iam attach-role-policy --role-name mistake-pr-db-lambda-role \
  --policy-arn arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole

# MASTER_URL 直接取现有 /mistake/DATABASE_URL 的值（它连的就是共享实例 postgres 库）
MASTER_URL="$(aws ssm get-parameter --name /mistake/DATABASE_URL --with-decryption --query Parameter.Value --output text)"
aws lambda create-function --function-name mistake-pr-db \
  --runtime provided.al2023 --architectures arm64 --handler bootstrap \
  --role arn:aws:iam::496251221975:role/mistake-pr-db-lambda-role \
  --zip-file fileb://fn.zip --timeout 30 \
  --environment "Variables={MASTER_URL=$MASTER_URL}" \
  --vpc-config SubnetIds=subnet-051776f835b1b44da,SecurityGroupIds=sg-034dd7da68ec36c3d
```
> **为什么 master URL 走 env 而非 SSM**：VPC 内 Lambda 默认无出网（无 NAT/VPC Endpoint 就到不了公网 SSM），但到**同 VPC 的 RDS** 走本地路由没问题。把 master URL 作为 KMS 加密的 env 传入，Lambda 只需连 RDS，最省事。SG 复用 `sg-034dd7da68ec36c3d`（已在 RDS SG 放行 5432）。
> 轮换：RDS 改密码后，`aws lambda update-function-configuration --environment ...` 更新即可。

### 4. GitHub 侧 Secrets / Variables（仓库 Settings → Secrets and variables → Actions）
- Secret `CLOUDFLARE_API_TOKEN`：与 SSM 里同一把 CF Pages Edit token。
- Variable `AWS_ROLE_ARN` = `arn:aws:iam::496251221975:role/mistake-gha-role`
- Variable `CLOUDFLARE_ACCOUNT_ID` = `f350f1151c3d19066f82fd0c42e8ecd2`
- API_KEY / DASHSCOPE 仍从 SSM 取（runner 有 SSM 读权限；DASHSCOPE 继续作为 ECS secret 注入，不进 GHA）。

---

## 二、复用改造：`deploy.sh` / `teardown.sh` 把 psql 换成 Lambda ✅ 已实现并实测

在 `deploy.sh` 顶部加一个开关与助手，其余不动：
```bash
# DB_VIA_LAMBDA=1 时用 Lambda 建库（GHA 场景）；否则用 psql（CodeBuild/VPC 场景）
pr_db() { # $1 = create|drop
  if [ "${DB_VIA_LAMBDA:-0}" = "1" ]; then
    aws lambda invoke --function-name mistake-pr-db \
      --payload "$(printf '{"action":"%s","pr":%s}' "$1" "$PR")" \
      --cli-binary-format raw-in-base64-out /dev/stdout >/dev/null
  else
    # —— 原来的 psql 分支保留 ——
    :
  fi
}
```
- `deploy.sh` 里「1. 建 per-PR 逻辑库」整段替换为：`[ "${DB_VIA_LAMBDA:-0}" = "1" ] && pr_db create || { 原 psql 逻辑 }`；随后把 per-PR `DATABASE_URL` 写 SSM 的逻辑保留（taskdef 仍作为 secret 引用它，Lambda 不管这个）。
- `teardown.sh` 里 DROP 那段同理换成 `pr_db drop`。
> 这样一套脚本两种执行环境通吃，CodeBuild 恢复了也能无缝切回（不设 `DB_VIA_LAMBDA` 即走 psql）。

## 三、工作流 `.github/workflows/pr-preview.yml` ✅ 已落地（下方为其内容）

```yaml
name: pr-preview
on:
  pull_request:
    types: [opened, synchronize, reopened, closed]
permissions:
  id-token: write   # OIDC
  contents: read
concurrency:              # 同一 PR 串行，避免并发部署打架
  group: pr-preview-${{ github.event.number }}
  cancel-in-progress: false
env:
  AWS_REGION: us-east-1
  ACCOUNT: "496251221975"
  CLUSTER: mistake
  ALB_LISTENER_ARN: arn:aws:elasticloadbalancing:us-east-1:496251221975:listener/app/mistake-alb/a48a24d45f5a8de7/ee90628b7c1563fa
  VPC_ID: vpc-0c36b43361100250a
  SUBNETS: subnet-051776f835b1b44da,subnet-0b62c3c3da2bf7fbd,subnet-0f3f95ddb4ab8d5c5
  TASK_SG: sg-0b23ad3efd38c816a
  BASE_DOMAIN: api.toton123.xyz
  PAGES_PROJECT: mistake
  PAGES_DOMAIN: mistake-381.pages.dev
  DB_VIA_LAMBDA: "1"

jobs:
  deploy:
    if: github.event.action != 'closed'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ vars.AWS_ROLE_ARN }}
          aws-region: us-east-1
      - uses: aws-actions/amazon-ecr-login@v2
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - name: Build & push arm64 image
        run: |
          PR=${{ github.event.number }}
          ECR=$ACCOUNT.dkr.ecr.$AWS_REGION.amazonaws.com
          docker buildx build --platform linux/arm64 \
            --build-arg GO_IMAGE=public.ecr.aws/docker/library/golang:1.26 \
            -t $ECR/mistake-server:pr-$PR --push apps/server-go
      - name: Orchestrate backend + frontend
        env:
          PR: ${{ github.event.number }}
          BRANCH: ${{ github.head_ref }}
          CLOUDFLARE_API_TOKEN: ${{ secrets.CLOUDFLARE_API_TOKEN }}
          CLOUDFLARE_ACCOUNT_ID: ${{ vars.CLOUDFLARE_ACCOUNT_ID }}
        run: bash deploy/aws/pr-preview/deploy.sh   # 复用！psql 段已切 Lambda

  teardown:
    if: github.event.action == 'closed'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ vars.AWS_ROLE_ARN }}
          aws-region: us-east-1
      - name: Teardown
        env:
          PR: ${{ github.event.number }}
        run: bash deploy/aws/pr-preview/teardown.sh
```
> `deploy.sh` 现在从环境变量取 `PR`/`BRANCH`（不再解析 `CODEBUILD_WEBHOOK_*`）——CodeBuild 版 buildspec 是先 `export PR=...` 再调脚本，这里在 workflow 里 `env:` 直接给，脚本无需改取值逻辑。构建镜像那步已在 workflow 里做，`deploy.sh` 里就别再 build（CodeBuild buildspec 里的 build 步骤本就在脚本外）。

## 四、arm64 太慢？改预览为 x86（可选，二选一）

buildx+QEMU 构建 arm64 大概 3–6 分钟。想更快就让**预览后端跑 x86**（不影响生产 arm64）：
- `taskdef.tpl.json` 的 `cpuArchitecture` 改 `X86_64`（可再做个 `taskdef.x86.tpl.json` 供 GHA 用）。
- workflow 构建改 `--platform linux/amd64`，Lambda 也可留 arm64 不受影响。
- 原生 x86 build 无需 QEMU，快很多。

## 五、验证 / 回滚

- 验证：改点东西提 PR → Actions 里 `deploy` job 绿 → `curl https://pr-<N>.api.toton123.xyz/health`、开 `<slug>.mistake-381.pages.dev`、`aws lambda invoke` 日志确认建库、关 PR 看 `teardown` job 全删。清单与 [README.md](README.md) 一致。
- 与 CodeBuild 并存：两套可同时挂在同一仓库（CodeBuild webhook + GHA workflow）。**别同时开**，否则一个 PR 触发两套部署互相打架。上线 GHA 前先 `aws codebuild delete-webhook --project-name mistake-pr-deploy`（和 teardown），或反之。
- 回滚到 CodeBuild：删/停 workflow，恢复 CodeBuild webhook，`deploy.sh` 不设 `DB_VIA_LAMBDA` 即自动走 psql。Lambda 可留着不碍事。

## 六、工作量对比（决策参考）

新增一次性活：OIDC provider、GHA 角色+信任、Go Lambda（写+建执行角色+VPC 部署）、GHA secrets、`deploy.sh` 加 20 行开关、一个 workflow。**并不比等工单省事**——若工单大概率能批，等它更快；GHA 的价值在于**彻底绕开 CodeBuild 账号封控**、且 runner 免费额度足、以后 CI 也好扩。建议：先开工单，同时把本手册备着；工单被拒或拖太久就按此落地。
