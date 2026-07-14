#!/usr/bin/env bash
# 为一个 PR 拉起/更新独立预览环境（幂等，可被 PR synchronize 反复调用）。
# 两种执行环境通吃：
#   - GitHub Actions：workflow 里 env 给 PR/BRANCH，镜像已用 buildx 推 ECR，DB_VIA_LAMBDA=1
#   - CodeBuild：buildspec-deploy.yml 导出 PR/BRANCH，镜像已推 ECR，不设 DB_VIA_LAMBDA（走 psql）
#
# 依赖环境变量：
#   PR                PR 号（GHA: ${{github.event.number}}；CodeBuild: 由 buildspec 解析）
#   BRANCH            源分支名（GHA: github.head_ref；CodeBuild: 去掉 refs/heads/）
#   ACCOUNT REGION    AWS 账号 / 区域
#   CLUSTER           ECS 集群（mistake）
#   ALB_LISTENER_ARN  443 监听器 ARN
#   VPC_ID            默认 VPC
#   SUBNETS           逗号分隔公有子网（给 ECS 服务）
#   TASK_SG           任务安全组（sg-0b23ad3efd38c816a）
#   BASE_DOMAIN       api.toton123.xyz
#   PAGES_PROJECT     Cloudflare Pages 项目名（mistake）
#   PAGES_DOMAIN      mistake-381.pages.dev
#   DB_VIA_LAMBDA     =1 时用 VPC Lambda 建/删库（GHA）；否则用 psql（CodeBuild）
#   DB_LAMBDA         Lambda 名，默认 mistake-pr-db
# SSM 取值：/mistake/DATABASE_URL(复用为 master) /mistake/API_KEY /mistake/CF_API_TOKEN
set -euo pipefail

: "${PR:?}" "${BRANCH:?}" "${ACCOUNT:?}" "${REGION:?}" "${CLUSTER:?}"
: "${ALB_LISTENER_ARN:?}" "${VPC_ID:?}" "${SUBNETS:?}" "${TASK_SG:?}"
: "${BASE_DOMAIN:?}" "${PAGES_PROJECT:?}" "${PAGES_DOMAIN:?}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"

DB_NAME="mistake_pr_${PR}"
SVC="mistake-pr-${PR}"
FAMILY="mistake-pr-${PR}"
TG_NAME="mistake-pr-${PR}-tg"
API_HOST="pr-${PR}.${BASE_DOMAIN}"                 # pr-123.api.toton123.xyz
API_URL="https://${API_HOST}"
# Cloudflare Pages 分支别名：小写、非字母数字转 -，长度<=28，与 CF slug 规则一致
SLUG="$(printf '%s' "$BRANCH" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/-/g; s/^-*//; s/-*$//' | cut -c1-28 | sed 's/-*$//')"
FRONT_URL="https://${SLUG}.${PAGES_DOMAIN}"

echo "== PR #${PR} branch=${BRANCH} slug=${SLUG}"
echo "   backend  ${API_URL}"
echo "   frontend ${FRONT_URL}"

ssm() { aws ssm get-parameter --name "$1" --with-decryption --query Parameter.Value --output text; }
# 复用生产 DATABASE_URL 当建库用的 master 连接串（它连的就是共享实例的 postgres 库，
# 主用户有 CREATEDB 权限）。deploy 时把末段库名替换成 mistake_pr_<N>。
RDS_MASTER_URL="$(ssm /mistake/DATABASE_URL)"
API_KEY="$(ssm /mistake/API_KEY)"

# 建/删 per-PR 逻辑库。DB_VIA_LAMBDA=1 走 VPC Lambda（GHA：runner 在 VPC 外连不到私有
# RDS）；否则用 psql（CodeBuild/VPC 内）。$1=create|drop。两条路都幂等。
pr_db() {
  if [ "${DB_VIA_LAMBDA:-0}" = "1" ]; then
    echo "== db $1 via lambda ${DB_LAMBDA:-mistake-pr-db} (pr=${PR})"
    local tmp err; tmp="$(mktemp)"
    err="$(aws lambda invoke --function-name "${DB_LAMBDA:-mistake-pr-db}" \
      --payload "$(printf '{"action":"%s","pr":%s}' "$1" "$PR")" \
      --cli-binary-format raw-in-base64-out "$tmp" \
      --query 'FunctionError' --output text)"
    echo "   result: $(cat "$tmp")"; rm -f "$tmp"
    [ "$err" = "None" ] || { echo "   lambda FunctionError=$err"; return 1; }
  elif [ "$1" = "create" ]; then
    if ! psql "$RDS_MASTER_URL" -tAc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" | grep -q 1; then
      echo "== create database ${DB_NAME}"; psql "$RDS_MASTER_URL" -c "CREATE DATABASE ${DB_NAME}"
    fi
  else
    psql "$RDS_MASTER_URL" -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='${DB_NAME}' AND pid<>pg_backend_pid()" >/dev/null 2>&1 || true
    psql "$RDS_MASTER_URL" -c "DROP DATABASE IF EXISTS ${DB_NAME}" || true
  fi
}

# ── 1. 建 per-PR 逻辑库（幂等）─────────────────────────────────────────
pr_db create
# 把 per-PR DATABASE_URL 写进 SSM（SecureString，供 taskdef 作为 secret 引用）
# 用 master 连接串同款用户/主机/参数，只替换库名。
DB_URL="$(printf '%s' "$RDS_MASTER_URL" | sed -E "s#(/)[^/?]+(\?|$)#\1${DB_NAME}\2#")"
aws ssm put-parameter --name "/mistake/pr/${PR}/DATABASE_URL" --type SecureString \
  --value "$DB_URL" --overwrite >/dev/null

# ── 2. 注册任务定义 ─────────────────────────────────────────────────────
TASKDEF_JSON="$(mktemp)"
sed -e "s#__PR__#${PR}#g" -e "s#__ACCOUNT__#${ACCOUNT}#g" \
    -e "s#__REGION__#${REGION}#g" -e "s#__CORS__#${FRONT_URL}#g" \
    "$HERE/taskdef.tpl.json" > "$TASKDEF_JSON"
aws ecs register-task-definition --cli-input-json "file://${TASKDEF_JSON}" >/dev/null
echo "== registered task definition ${FAMILY}"

# ── 3. Target Group（无则建）───────────────────────────────────────────
TG_ARN="$(aws elbv2 describe-target-groups --names "$TG_NAME" \
  --query 'TargetGroups[0].TargetGroupArn' --output text 2>/dev/null || true)"
if [ -z "$TG_ARN" ] || [ "$TG_ARN" = "None" ]; then
  TG_ARN="$(aws elbv2 create-target-group --name "$TG_NAME" \
    --protocol HTTP --port 3000 --vpc-id "$VPC_ID" --target-type ip \
    --health-check-path /health \
    --query 'TargetGroups[0].TargetGroupArn' --output text)"
  echo "== created target group ${TG_NAME}"
fi

# ── 4. ALB 监听规则（按 Host 头，无则建）───────────────────────────────
RULE_ARN="$(aws elbv2 describe-rules --listener-arn "$ALB_LISTENER_ARN" \
  --query "Rules[?Conditions[?contains(HostHeaderConfig.Values, '${API_HOST}')]].RuleArn | [0]" \
  --output text 2>/dev/null || true)"
if [ -z "$RULE_ARN" ] || [ "$RULE_ARN" = "None" ]; then
  # 取一个未占用的优先级（从 1000 起找空位，避开已有 prod 默认规则）
  USED="$(aws elbv2 describe-rules --listener-arn "$ALB_LISTENER_ARN" \
    --query 'Rules[?Priority!=`default`].Priority' --output text)"
  PRIO=1000; while printf '%s\n' $USED | grep -qx "$PRIO"; do PRIO=$((PRIO+1)); done
  aws elbv2 create-rule --listener-arn "$ALB_LISTENER_ARN" --priority "$PRIO" \
    --conditions "Field=host-header,HostHeaderConfig={Values=[${API_HOST}]}" \
    --actions "Type=forward,TargetGroupArn=${TG_ARN}" >/dev/null
  echo "== created listener rule host=${API_HOST} priority=${PRIO}"
fi

# ── 5. ECS 服务（无则建，有则强制新部署）───────────────────────────────
NETCFG="awsvpcConfiguration={subnets=[${SUBNETS}],securityGroups=[${TASK_SG}],assignPublicIp=ENABLED}"
if aws ecs describe-services --cluster "$CLUSTER" --services "$SVC" \
     --query 'services[0].status' --output text 2>/dev/null | grep -q ACTIVE; then
  aws ecs update-service --cluster "$CLUSTER" --service "$SVC" \
    --task-definition "$FAMILY" --force-new-deployment >/dev/null
  echo "== updated service ${SVC}"
else
  aws ecs create-service --cluster "$CLUSTER" --service-name "$SVC" \
    --task-definition "$FAMILY" --desired-count 1 --launch-type FARGATE \
    --network-configuration "$NETCFG" \
    --load-balancers "targetGroupArn=${TG_ARN},containerName=server,containerPort=3000" >/dev/null
  echo "== created service ${SVC}"
fi

# ── 6. 等目标健康（最多 ~5 分钟）────────────────────────────────────────
echo "== waiting for target health…"
for i in $(seq 1 30); do
  STATE="$(aws elbv2 describe-target-health --target-group-arn "$TG_ARN" \
    --query 'TargetHealthDescriptions[0].TargetHealth.State' --output text 2>/dev/null || echo none)"
  echo "   [$i] target=${STATE}"
  [ "$STATE" = "healthy" ] && break
  sleep 10
done

# ── 7. 构建 + 部署前端预览（注入该 PR 的后端 URL）──────────────────────
echo "== build & deploy frontend preview"
export CLOUDFLARE_API_TOKEN="$(ssm /mistake/CF_API_TOKEN)"
# 账号下有多个 Pages 项目时，wrangler 需知道 account id；CodeBuild 项目里配普通 env 即可
export CLOUDFLARE_ACCOUNT_ID="${CLOUDFLARE_ACCOUNT_ID:-}"
( cd "$REPO_ROOT" && npm ci --no-audit --no-fund )
( cd "$REPO_ROOT/apps/web" \
  && VITE_SERVER_URL="$API_URL" VITE_API_KEY="$API_KEY" npm run build \
  && npx wrangler@4 pages deploy dist --project-name "$PAGES_PROJECT" --branch "$SLUG" )

echo "== DONE"
echo "   backend  ${API_URL}"
echo "   frontend ${FRONT_URL}"
