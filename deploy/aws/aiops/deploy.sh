#!/usr/bin/env bash
# 构建 arm64 agent -> 传 S3 -> 部署 CloudFormation 栈 mistake-aiops。
# 前置：GitHub PAT 已写入 SSM（见 README）；DashScope key 复用 /mistake/DASHSCOPE_API_KEY。
set -euo pipefail

REGION="${REGION:-us-east-1}"
ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
APP="${APP:-mistake}"
STACK="${STACK:-mistake-aiops}"
ALARM_TOPIC_ARN="${ALARM_TOPIC_ARN:?set ALARM_TOPIC_ARN to the SNS alarm topic}"
QUEUE_URL="${QUEUE_URL:-https://sqs.$REGION.amazonaws.com/$ACCOUNT/$APP-recognition}"
DLQ_URL="${DLQ_URL:-https://sqs.$REGION.amazonaws.com/$ACCOUNT/$APP-recognition-dlq}"
GITHUB_REPO="${GITHUB_REPO:-Tearl/mistake}"
# 复用 SAM 托管桶存放代码；也可传自己的 CODE_BUCKET。
CODE_BUCKET="${CODE_BUCKET:-aws-sam-cli-managed-default-samclisourcebucket-$(aws s3api list-buckets --query "Buckets[?starts_with(Name,'aws-sam-cli-managed-default')].Name | [0]" --output text | sed 's/.*samclisourcebucket-//')}"
SHA="$(git -C "$(dirname "$0")/../../.." rev-parse --short HEAD)"
KEY="aiops/agent-$SHA.zip"

here="$(cd "$(dirname "$0")" && pwd)"
echo "== build arm64 bootstrap =="
( cd "$here/agent" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bootstrap . )
( cd "$here/agent" && zip -q -j "/tmp/agent-$SHA.zip" bootstrap )

echo "== upload s3://$CODE_BUCKET/$KEY =="
aws s3 cp "/tmp/agent-$SHA.zip" "s3://$CODE_BUCKET/$KEY" --region "$REGION"

echo "== deploy stack $STACK =="
aws cloudformation deploy \
  --region "$REGION" \
  --stack-name "$STACK" \
  --template-file "$here/template.yaml" \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    AppName="$APP" \
    CodeS3Bucket="$CODE_BUCKET" \
    CodeS3Key="$KEY" \
    AlarmTopicArn="$ALARM_TOPIC_ARN" \
    GitHubRepo="$GITHUB_REPO" \
    QueueUrl="$QUEUE_URL" \
    DlqUrl="$DLQ_URL"

echo "== done =="
aws cloudformation describe-stacks --region "$REGION" --stack-name "$STACK" \
  --query "Stacks[0].Outputs" --output table
