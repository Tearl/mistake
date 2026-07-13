# 拾错后端上线 AWS —— 手动 Runbook

后端跑在 ECS Fargate，前 ALB(HTTPS) 入口，图片存 S3，复用现有 RDS，ECS 放进 **RDS 所在的 VPC**。前端另走 Cloudflare Pages（见最后一节）。

> 约定：命令里的占位大写变量请先按你的实际值替换。控制台路径给的是「服务 → 页面」，也给了等价 `aws` CLI。区域以你 RDS 所在 region 为准。

```bash
# —— 先设置这些环境变量，后面命令直接复用 ——
export AWS_REGION=ap-southeast-1              # 改成你 RDS 的 region
export ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
export APP=mistake
export DOMAIN=api.yourdomain.com             # 后端对外域名
export BUCKET=mistake-uploads-$ACCOUNT       # S3 桶名（需全局唯一）
export ECR_REPO=mistake-server
```

前置：本机装好 `aws` CLI（`aws configure` 配好凭证）、Docker Desktop（构建镜像）。

---

## 1. 摸清现网（记下这些值）
```bash
# 找到 RDS 实例的 VPC、子网组、安全组、Endpoint
aws rds describe-db-instances --query \
 "DBInstances[].{id:DBInstanceIdentifier,vpc:DBSubnetGroup.VpcId,ep:Endpoint.Address,sg:VpcSecurityGroups[0].VpcSecurityGroupId}"
# 列出该 VPC 的子网，看哪些是公有（有到 IGW 的路由）
aws ec2 describe-subnets --filters Name=vpc-id,Values=VPC_ID \
 --query "Subnets[].{id:SubnetId,az:AvailabilityZone,public:MapPublicIpOnLaunch}"
```
本项目实测：账号里**没有现成 RDS**（只有一个无关的 Aurora 快照），故用**默认 VPC** `vpc-0c36b43361100250a`（自带跨 AZ 公有子网 + IGW）。记下默认 VPC 的 ≥2 个**公有子网** `PUB_SUBNET_A/B`。

## 2. 新建 RDS PostgreSQL（控制台，放默认 VPC）
RDS → Create database：
- Engine PostgreSQL 16.x；模板 Free tier（无则 Dev/Test）
- 规格 db.t4g.micro；存储 gp3 20GiB，关 autoscaling
- Connectivity：**默认 VPC**；Public access **No**；新建安全组 `mistake-rds-sg`
- **Additional configuration → Initial database name 填 `mistake`**（关键：库随实例一起创建，无需再手动 createdb）
- 建好记下：`RDS_ENDPOINT`、master 用户/密码、`RDS_SG`(mistake-rds-sg 的 sg-id)

> 因为选了「初始数据库名 = mistake」+ 应用启动自动建表，本步不需要再连库跑 SQL。连接用户是 RDS 主用户，有建表 + `CREATE EXTENSION pgcrypto` 权限。

## 3. S3 桶（存错题图，公开读）
```bash
aws s3api create-bucket --bucket $BUCKET --region $AWS_REGION \
  --create-bucket-configuration LocationConstraint=$AWS_REGION
# 关掉「阻止公有访问」以便挂公读策略
aws s3api put-public-access-block --bucket $BUCKET \
  --public-access-block-configuration BlockPublicAcls=false,IgnorePublicAcls=false,BlockPublicPolicy=false,RestrictPublicBuckets=false
# 只对 uploads/* 公开读
aws s3api put-bucket-policy --bucket $BUCKET --policy "{
  \"Version\":\"2012-10-17\",
  \"Statement\":[{\"Sid\":\"PublicReadUploads\",\"Effect\":\"Allow\",\"Principal\":\"*\",
    \"Action\":\"s3:GetObject\",\"Resource\":\"arn:aws:s3:::$BUCKET/uploads/*\"}]}"
```
> `<img>` 直接读 S3 不需要桶 CORS。想更稳/加缓存可后续在前面挂 CloudFront。

## 4. ECR：建仓库并推镜像（arm64）
```bash
aws ecr create-repository --repository-name $ECR_REPO
aws ecr get-login-password | docker login --username AWS --password-stdin $ACCOUNT.dkr.ecr.$AWS_REGION.amazonaws.com

cd apps/server-go
# M 芯片本机原生构建 arm64（任务定义里也要设 ARM64，两者必须一致）
docker build --platform linux/arm64 -t $ECR_REPO .
docker tag $ECR_REPO:latest $ACCOUNT.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO:latest
docker push $ACCOUNT.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO:latest
```

## 5. SSM Parameter Store：存密钥（SecureString）
```bash
aws ssm put-parameter --name /$APP/DATABASE_URL --type SecureString \
  --value "postgres://MASTER_USER:PASS@$RDS_ENDPOINT:5432/mistake?sslmode=require"
aws ssm put-parameter --name /$APP/DASHSCOPE_API_KEY --type SecureString --value "sk-xxx"
aws ssm put-parameter --name /$APP/API_KEY --type SecureString --value "$(openssl rand -hex 24)"
```
记下最后这个 `API_KEY` 明文——前端 Cloudflare Pages 也要用同一个。

## 6. IAM 两个角色
信任主体都是 `ecs-tasks.amazonaws.com`。
- **执行角色**（ECS 拉镜像/取密钥/写日志）：附管理策略 `AmazonECSTaskExecutionRolePolicy`，再加内联允许 `ssm:GetParameters`（对上面 3 个参数的 ARN）和 `kms:Decrypt`（SecureString 默认用 `alias/aws/ssm`）。
- **任务角色**（应用代码访问 S3）：内联允许 `s3:PutObject/GetObject/DeleteObject`，资源 `arn:aws:s3:::$BUCKET/uploads/*`。

控制台：IAM → 角色 → 创建角色 → 受信任实体「AWS 服务 / Elastic Container Service Task」。记下两个 ARN：`EXEC_ROLE_ARN`、`TASK_ROLE_ARN`。

## 7. 安全组（三条）
```bash
# ALB SG：放行公网 80/443
aws ec2 create-security-group --group-name $APP-alb --description "alb" --vpc-id VPC_ID   # 记 ALB_SG
aws ec2 authorize-security-group-ingress --group-id ALB_SG --protocol tcp --port 443 --cidr 0.0.0.0/0
aws ec2 authorize-security-group-ingress --group-id ALB_SG --protocol tcp --port 80  --cidr 0.0.0.0/0

# ECS 任务 SG：只放行来自 ALB 的 3000
aws ec2 create-security-group --group-name $APP-task --description "task" --vpc-id VPC_ID  # 记 TASK_SG
aws ec2 authorize-security-group-ingress --group-id TASK_SG --protocol tcp --port 3000 --source-group ALB_SG

# 在 RDS 的 SG 上放行来自任务 SG 的 5432
aws ec2 authorize-security-group-ingress --group-id RDS_SG --protocol tcp --port 5432 --source-group TASK_SG
```

## 8. ACM 证书（DNS 验证）
```bash
aws acm request-certificate --domain-name $DOMAIN --validation-method DNS   # 记 CERT_ARN
aws acm describe-certificate --certificate-arn CERT_ARN \
  --query "Certificate.DomainValidationOptions[0].ResourceRecord"
```
把返回的 CNAME（name/value）加到你的 DNS 商，等状态变 `ISSUED`。

## 9. ALB + Target Group + 监听器
```bash
# 面向公网的 ALB，放两个公有子网
aws elbv2 create-load-balancer --name $APP-alb --type application --scheme internet-facing \
  --subnets PUB_SUBNET_A PUB_SUBNET_B --security-groups ALB_SG            # 记 ALB_ARN, ALB_DNS

# 目标组：IP 型（Fargate），HTTP:3000，健康检查 /health
aws elbv2 create-target-group --name $APP-tg --protocol HTTP --port 3000 \
  --vpc-id VPC_ID --target-type ip --health-check-path /health           # 记 TG_ARN

# 443 监听器挂证书转发 TG；80 重定向到 443
aws elbv2 create-listener --load-balancer-arn ALB_ARN --protocol HTTPS --port 443 \
  --certificates CertificateArn=CERT_ARN \
  --default-actions Type=forward,TargetGroupArn=TG_ARN
aws elbv2 create-listener --load-balancer-arn ALB_ARN --protocol HTTP --port 80 \
  --default-actions '[{"Type":"redirect","RedirectConfig":{"Protocol":"HTTPS","Port":"443","StatusCode":"HTTP_301"}}]'
```

## 10. 任务出网
任务要能出公网（拉 ECR、访问 S3/SSM、调 DashScope）。**学习期最简**：任务放**公有子网**并 `assignPublicIp=ENABLED`（入站仍被 TASK_SG 限制，只有 ALB 能进）。更安全的做法：私有子网 + NAT 网关（有成本），S3/ECR/SSM 再用 VPC Endpoint 省 NAT。

## 11. ECS 集群
```bash
aws ecs create-cluster --cluster-name $APP
# 顺手建日志组
aws logs create-log-group --log-group-name /ecs/$APP
```

## 12. 任务定义（Fargate, ARM64）
把下面存成 `taskdef.json`（替换占位），注册：
```json
{
  "family": "mistake",
  "requiresCompatibilities": ["FARGATE"],
  "networkMode": "awsvpc",
  "cpu": "256", "memory": "512",
  "runtimePlatform": { "cpuArchitecture": "ARM64", "operatingSystemFamily": "LINUX" },
  "executionRoleArn": "EXEC_ROLE_ARN",
  "taskRoleArn": "TASK_ROLE_ARN",
  "containerDefinitions": [{
    "name": "server",
    "image": "ACCOUNT.dkr.ecr.REGION.amazonaws.com/mistake-server:latest",
    "portMappings": [{ "containerPort": 3000 }],
    "essential": true,
    "environment": [
      { "name": "PORT", "value": "3000" },
      { "name": "STORAGE", "value": "s3" },
      { "name": "S3_BUCKET", "value": "BUCKET" },
      { "name": "AWS_REGION", "value": "REGION" },
      { "name": "S3_PUBLIC_BASE_URL", "value": "https://BUCKET.s3.REGION.amazonaws.com" },
      { "name": "CORS_ORIGIN", "value": "https://你的前端域名" }
    ],
    "secrets": [
      { "name": "DATABASE_URL",      "valueFrom": "arn:aws:ssm:REGION:ACCOUNT:parameter/mistake/DATABASE_URL" },
      { "name": "DASHSCOPE_API_KEY", "valueFrom": "arn:aws:ssm:REGION:ACCOUNT:parameter/mistake/DASHSCOPE_API_KEY" },
      { "name": "API_KEY",           "valueFrom": "arn:aws:ssm:REGION:ACCOUNT:parameter/mistake/API_KEY" }
    ],
    "logConfiguration": {
      "logDriver": "awslogs",
      "options": { "awslogs-group": "/ecs/mistake", "awslogs-region": "REGION", "awslogs-stream-prefix": "server" }
    }
  }]
}
```
```bash
aws ecs register-task-definition --cli-input-json file://taskdef.json
```

## 13. ECS 服务（关联 ALB）
```bash
aws ecs create-service --cluster $APP --service-name $APP --task-definition mistake \
  --desired-count 1 --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[PUB_SUBNET_A,PUB_SUBNET_B],securityGroups=[TASK_SG],assignPublicIp=ENABLED}" \
  --load-balancers "targetGroupArn=TG_ARN,containerName=server,containerPort=3000"
```
任务起来后会**自动跑迁移建表 + seed**。等目标组健康检查变 healthy：
```bash
aws elbv2 describe-target-health --target-group-arn TG_ARN
```
不健康就看日志：`aws logs tail /ecs/$APP --follow`。

## 14. DNS
在你的 DNS 商把 `api.yourdomain.com` 加一条 **CNAME → ALB_DNS**（`aws elbv2 describe-load-balancers` 里的 DNSName）。

## 15. 验证后端
```bash
curl https://$DOMAIN/health                                   # ok / 200
curl -H "X-API-Key: 你的API_KEY" https://$DOMAIN/api/stats     # 200 + JSON
curl https://$DOMAIN/api/stats                                 # 401（无 key 被挡）
```
再传一张图测 S3 + AI：`POST /api/upload` → 对象进 S3、`imageFileID` 是 S3 URL → `POST /api/recognize` 出识别结果。

---

## 16. 前端 → Cloudflare Pages
1. Pages 项目连本仓库，构建设置：
   - 构建命令：`npm install && npm run build --workspace web`
   - 输出目录：`apps/web/dist`
2. 环境变量（Production，构建期）：
   - `VITE_SERVER_URL = https://api.yourdomain.com`
   - `VITE_API_KEY   = 与后端同一个 API_KEY`
3. `apps/web/public/_redirects` 已内置 `/*  /index.html  200`（SPA 深链回退）。
4. 部署后把后端任务定义里的 `CORS_ORIGIN` 改成 Pages 的正式域名并更新服务。
5. 浏览器过一遍：首页/复习/上传(真识别)/统计/列表导出/详情；确认请求打到 `https://api.yourdomain.com`、带 `X-API-Key`、无 CORS 报错。

## 注意
- 共享密钥会打进前端包，只挡随手直调；要防滥用再加限流/WAF 或把 API 也套 Cloudflare 代理。
- 镜像架构（arm64）必须与任务 `runtimePlatform` 一致，否则任务起不来。
- DashScope 是阿里云公网，AWS 出网到它有跨境延迟；任务必须能出网。

---

# 附录 A：本次实际部署的真值（2026-07 上线）

- 账号 `496251221975` · 区域 `us-east-1`
- 前端：`https://app.toton123.xyz`（Cloudflare Pages 项目 `mistake`，默认域 `mistake-381.pages.dev`）
- 后端：`https://api.toton123.xyz`

| 资源 | 实际值 |
|---|---|
| VPC | `vpc-0c36b43361100250a`（**默认 VPC**；账号本无现成 RDS） |
| 公有子网 | `subnet-051776f835b1b44da`(1a) `subnet-0b62c3c3da2bf7fbd`(1b) `subnet-0f3f95ddb4ab8d5c5`(1c) |
| RDS | 实例 `mistake-db`，标准 postgres 18 / db.t4g.micro；**连的是自带 `postgres` 库**（不是 `mistake`）；SG `sg-083ab0e47ea42b8fd` |
| S3 | `mistake-uploads-496251221975`（`uploads/*` 公读） |
| ECR | `496251221975.dkr.ecr.us-east-1.amazonaws.com/mistake-server:latest`（arm64） |
| ECS | 集群 `mistake` · 服务 `mistake` · 任务定义 family `mistake`（当前 rev 3）· Fargate 256/512 ARM64 |
| ALB | `mistake-alb`，DNS `mistake-alb-665430821.us-east-1.elb.amazonaws.com`；监听 443(ACM)+80→443 |
| Target Group | `mistake-tg`（IP, HTTP:3000, 健康检查 `/health`） |
| ACM | `arn:aws:acm:us-east-1:496251221975:certificate/502200bb-f4c5-4c97-bf15-88cddeac206e`（api.toton123.xyz） |
| SSM | `/mistake/DATABASE_URL`、`/mistake/DASHSCOPE_API_KEY`、`/mistake/API_KEY`（均 SecureString） |
| IAM | 执行角色 `mistake-ecs-exec-role`、任务角色 `mistake-task-role` |
| 安全组 | ALB `sg-05740a66af45857ee`、任务 `sg-0b23ad3efd38c816a` |
| DNS(Cloudflare) | `api` 灰云 CNAME→ALB；`app` 由 Pages 自动管理（橙云） |

> Docker 构建（国内网络）：`docker build --platform linux/arm64 --build-arg GO_IMAGE=m.daocloud.io/docker.io/library/golang:1.26 -t mistake-server .`

# 附录 B：重新部署

**后端改了代码**（apps/server-go）：
```bash
export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"
cd apps/server-go
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 496251221975.dkr.ecr.us-east-1.amazonaws.com
docker build --platform linux/arm64 --build-arg GO_IMAGE=m.daocloud.io/docker.io/library/golang:1.26 -t mistake-server .
docker tag mistake-server:latest 496251221975.dkr.ecr.us-east-1.amazonaws.com/mistake-server:latest
docker push 496251221975.dkr.ecr.us-east-1.amazonaws.com/mistake-server:latest
aws ecs update-service --cluster mistake --service mistake --force-new-deployment
```

**只改后端环境变量/CORS**（不改代码）：编辑 `taskdef.json` → 注册新 rev → 指过去：
```bash
aws ecs register-task-definition --cli-input-json file://taskdef.json
aws ecs update-service --cluster mistake --service mistake --task-definition mistake --force-new-deployment
```

**前端改了代码**（apps/web）：
```bash
cd apps/web
VITE_SERVER_URL=https://api.toton123.xyz VITE_API_KEY=<SSM里的 /mistake/API_KEY> npm run build
npx wrangler@4 pages deploy dist --project-name mistake --branch main
```

**看日志 / 排查**：
```bash
aws logs tail /ecs/mistake --since 10m --follow
aws ecs describe-services --cluster mistake --services mistake --query "services[0].deployments"
aws elbv2 describe-target-health --target-group-arn <mistake-tg 的 ARN>
```

# 附录 C：停机 / 省钱 / 删除

- **临时停后端**（省 Fargate，秒级恢复）：`aws ecs update-service --cluster mistake --service mistake --desired-count 0`；恢复改回 `--desired-count 1`。
- **停 RDS**（最多停 7 天会自动重启）：控制台 RDS → mistake-db → Actions → Stop temporarily。
- **ALB 停不了**（按小时计费），要省这块只能删 ALB（之后重建）。
- **整套删除**顺序：ECS 服务 → ECS 集群 → ALB+监听器+TG → RDS（删时可跳过快照）→ S3（先清空）→ ECR → ACM 证书 → SSM 参数 → IAM 角色 → 安全组。Cloudflare 侧删 Pages 项目 + `api`/`app` 两条 DNS 记录。
