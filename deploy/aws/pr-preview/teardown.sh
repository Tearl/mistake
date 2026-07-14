#!/usr/bin/env bash
# 销毁一个 PR 的预览环境（PR closed/merged 时由 buildspec-teardown.yml 调用）。
# 反向拆除：ECS 服务 → 监听规则 → Target Group → per-PR SSM → 逻辑库 → ECR tag。
# 幂等：每一步都容忍资源已不存在。
#
# 依赖环境变量：PR ACCOUNT REGION CLUSTER ALB_LISTENER_ARN BASE_DOMAIN
#   DB_VIA_LAMBDA  =1 时用 VPC Lambda 删库（GHA）；否则用 psql（CodeBuild）
#   DB_LAMBDA      Lambda 名，默认 mistake-pr-db
# SSM 取值（仅 psql 模式）：/mistake/DATABASE_URL（复用为 master 连接串）
set -euo pipefail

: "${PR:?}" "${ACCOUNT:?}" "${REGION:?}" "${CLUSTER:?}" "${ALB_LISTENER_ARN:?}" "${BASE_DOMAIN:?}"

DB_NAME="mistake_pr_${PR}"
SVC="mistake-pr-${PR}"
FAMILY="mistake-pr-${PR}"
TG_NAME="mistake-pr-${PR}-tg"
API_HOST="pr-${PR}.${BASE_DOMAIN}"

echo "== teardown PR #${PR}"

# 1. ECS 服务：先缩到 0 再删
if aws ecs describe-services --cluster "$CLUSTER" --services "$SVC" \
     --query 'services[0].status' --output text 2>/dev/null | grep -q ACTIVE; then
  aws ecs update-service --cluster "$CLUSTER" --service "$SVC" --desired-count 0 >/dev/null || true
  aws ecs delete-service --cluster "$CLUSTER" --service "$SVC" --force >/dev/null || true
  echo "   deleted service ${SVC}"
fi

# 2. 监听规则（按 Host 头找）
RULE_ARN="$(aws elbv2 describe-rules --listener-arn "$ALB_LISTENER_ARN" \
  --query "Rules[?Conditions[?contains(HostHeaderConfig.Values, '${API_HOST}')]].RuleArn | [0]" \
  --output text 2>/dev/null || true)"
if [ -n "$RULE_ARN" ] && [ "$RULE_ARN" != "None" ]; then
  aws elbv2 delete-rule --rule-arn "$RULE_ARN" >/dev/null || true
  echo "   deleted listener rule ${API_HOST}"
fi

# 3. Target Group（删规则后才能删）
TG_ARN="$(aws elbv2 describe-target-groups --names "$TG_NAME" \
  --query 'TargetGroups[0].TargetGroupArn' --output text 2>/dev/null || true)"
if [ -n "$TG_ARN" ] && [ "$TG_ARN" != "None" ]; then
  aws elbv2 delete-target-group --target-group-arn "$TG_ARN" >/dev/null || true
  echo "   deleted target group ${TG_NAME}"
fi

# 4. 反注册任务定义所有修订（可选，避免堆积）
for arn in $(aws ecs list-task-definitions --family-prefix "$FAMILY" --query 'taskDefinitionArns' --output text 2>/dev/null); do
  aws ecs deregister-task-definition --task-definition "$arn" >/dev/null 2>&1 || true
done

# 5. per-PR SSM 参数
aws ssm delete-parameter --name "/mistake/pr/${PR}/DATABASE_URL" >/dev/null 2>&1 || true

# 6. 逻辑库：DB_VIA_LAMBDA=1 走 VPC Lambda，否则 psql（先踢连接再 DROP）
if [ "${DB_VIA_LAMBDA:-0}" = "1" ]; then
  aws lambda invoke --function-name "${DB_LAMBDA:-mistake-pr-db}" \
    --payload "$(printf '{"action":"drop","pr":%s}' "$PR")" \
    --cli-binary-format raw-in-base64-out /dev/stdout >/dev/null 2>&1 || true
else
  RDS_MASTER_URL="$(aws ssm get-parameter --name /mistake/DATABASE_URL --with-decryption --query Parameter.Value --output text)"
  psql "$RDS_MASTER_URL" -c \
    "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='${DB_NAME}' AND pid<>pg_backend_pid()" >/dev/null 2>&1 || true
  psql "$RDS_MASTER_URL" -c "DROP DATABASE IF EXISTS ${DB_NAME}" >/dev/null 2>&1 || true
fi
echo "   dropped database ${DB_NAME}"

# 7. ECR 镜像 tag
aws ecr batch-delete-image --repository-name mistake-server \
  --image-ids imageTag="pr-${PR}" >/dev/null 2>&1 || true

echo "== teardown DONE (Cloudflare Pages 预览部署保留，可在 CF 控制台按需删除)"
