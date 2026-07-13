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
记下：`VPC_ID`、`RDS_ENDPOINT`、`RDS_SG`（RDS 的安全组）、≥2 个 AZ 的**公有子网** `PUB_SUBNET_A/B`。
- ⚠️ 若该 VPC 没有公有子网（ALB 需要），得先建 IGW + 公有子网 + 路由，或把 ALB 放到有公网出口的子网。

## 2. RDS 建库
后端启动会自动建表，但**database 本身要先存在**。用现有 db 名也行（那就跳过建库，直接在第 5 步把 DATABASE_URL 指过去）。若要新建 `mistake` 库，需要能连到 RDS（临时开 public access，或用 VPC 内的堡垒机 / 一次性 ECS 任务）：
```bash
psql "postgres://MASTER_USER:PASS@$RDS_ENDPOINT:5432/postgres?sslmode=require" -c "CREATE DATABASE mistake;"
```
连接用户需有建表 + `CREATE EXTENSION pgcrypto` 权限（RDS 主用户满足）。

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
