// mistake-aiops-agent：轻量 AI 运维大脑。
//
// 两种触发：
//   1) SNS 告警（订阅 CloudWatch 告警主题）——有事件就诊断。
//   2) EventBridge 定时「主动巡检」——按计划体检，只有真出问题才建工单。
//
// 流程：采集上下文（告警状态 / SQS·DLQ 深度 / ECS 部署状态 / 近期错误日志）
//   → 调通义千问 qwen-plus 产出结构化诊断 → 满足条件时在 GitHub 建 Issue（带去重）。
//
// 设计约束：只读诊断，绝不改生产；密钥（DashScope key、GitHub PAT）运行时从
// SSM SecureString 取，不进环境变量。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const dashscopeURL = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"

// config 是非密钥配置；密钥用 *_PARAM 指向 SSM 参数名，运行时解密拉取。
type config struct {
	LogGroup       string // /ecs/mistake
	Cluster        string // mistake
	Service        string // mistake
	QueueURL       string
	DLQURL         string
	AlarmNames     []string
	Model          string // qwen-plus
	GitHubRepo     string // owner/repo
	DashKeyParam   string // /mistake/DASHSCOPE_API_KEY
	GitHubTokParam string // /mistake/GITHUB_TOKEN
}

func loadConfig() config {
	return config{
		LogGroup:       env("LOG_GROUP", "/ecs/mistake"),
		Cluster:        env("ECS_CLUSTER", "mistake"),
		Service:        env("ECS_SERVICE", "mistake"),
		QueueURL:       env("SQS_QUEUE_URL", ""),
		DLQURL:         env("SQS_DLQ_URL", ""),
		AlarmNames:     splitCSV(env("ALARM_NAMES", "")),
		Model:          env("MODEL", "qwen-plus"),
		GitHubRepo:     env("GITHUB_REPO", ""),
		DashKeyParam:   env("DASHSCOPE_KEY_PARAM", "/mistake/DASHSCOPE_API_KEY"),
		GitHubTokParam: env("GITHUB_TOKEN_PARAM", "/mistake/GITHUB_TOKEN"),
	}
}

// opsContext 是喂给模型的运维快照。
type opsContext struct {
	Trigger       string            `json:"trigger"` // "alarm" | "scan"
	AlarmName     string            `json:"alarmName,omitempty"`
	AlarmStates   map[string]string `json:"alarmStates"` // name -> OK/ALARM/INSUFFICIENT_DATA
	QueueDepth    string            `json:"queueDepth"`
	QueueInFlight string            `json:"queueInFlight"`
	DLQDepth      string            `json:"dlqDepth"`
	Deployment    string            `json:"deployment"`   // rolloutState running/desired
	RecentErrors  []string          `json:"recentErrors"` // 近期错误日志片段
	Notes         []string          `json:"notes,omitempty"`
}

func (c opsContext) anyAlarming() bool {
	for _, s := range c.AlarmStates {
		if s == "ALARM" {
			return true
		}
	}
	return false
}

// diagnosis 是模型返回的结构化诊断。
type diagnosis struct {
	Severity    string   `json:"severity"` // info | warning | high | critical
	Summary     string   `json:"summary"`
	RootCause   string   `json:"rootCause"`
	Suggestions []string `json:"suggestions"`
	Security    bool     `json:"security"`
}

func handler(ctx context.Context, raw json.RawMessage) error {
	cfg := loadConfig()
	trigger, alarmName := parseTrigger(raw)
	log.Printf("event=agent_start trigger=%s alarm=%q", trigger, alarmName)

	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	oc := gatherContext(ctx, awsCfg, cfg, trigger, alarmName)

	// 主动巡检且一切正常：不打扰，直接结束。
	if trigger == "scan" && !oc.anyAlarming() {
		log.Printf("event=scan_healthy alarms=%v", oc.AlarmStates)
		return nil
	}

	dashKey, err := getParam(ctx, awsCfg, cfg.DashKeyParam)
	if err != nil {
		return fmt.Errorf("read dashscope key: %w", err)
	}
	diag, err := diagnose(ctx, cfg, dashKey, oc)
	if err != nil {
		return fmt.Errorf("diagnose: %w", err)
	}
	log.Printf("event=diagnosed severity=%s security=%t summary=%q", diag.Severity, diag.Security, diag.Summary)

	if !shouldFileTicket(trigger, diag, oc) {
		log.Printf("event=ticket_skipped reason=below_threshold severity=%s", diag.Severity)
		return nil
	}

	tok, err := getParam(ctx, awsCfg, cfg.GitHubTokParam)
	if err != nil {
		return fmt.Errorf("read github token: %w", err)
	}
	fp := fingerprint(trigger, alarmName)
	if url, dup := findOpenIssue(ctx, cfg.GitHubRepo, tok, fp); dup {
		log.Printf("event=ticket_deduped fingerprint=%s existing=%s", fp, url)
		return nil
	}
	url, err := createIssue(ctx, cfg.GitHubRepo, tok, fp, diag, oc)
	if err != nil {
		return fmt.Errorf("create issue: %w", err)
	}
	log.Printf("event=ticket_created url=%s", url)
	return nil
}

// parseTrigger 区分 SNS 告警事件与定时巡检事件。
func parseTrigger(raw json.RawMessage) (trigger, alarmName string) {
	var probe struct {
		Records []struct {
			SNS struct {
				Message string `json:"Message"`
			} `json:"Sns"`
		} `json:"Records"`
	}
	if json.Unmarshal(raw, &probe) == nil && len(probe.Records) > 0 {
		var msg struct {
			AlarmName string `json:"AlarmName"`
		}
		_ = json.Unmarshal([]byte(probe.Records[0].SNS.Message), &msg)
		return "alarm", msg.AlarmName
	}
	return "scan", ""
}

// gatherContext 尽力采集运维快照；单项失败降级为 Notes，不中断整体。
func gatherContext(ctx context.Context, awsCfg aws.Config, cfg config, trigger, alarmName string) opsContext {
	oc := opsContext{Trigger: trigger, AlarmName: alarmName, AlarmStates: map[string]string{}}

	if len(cfg.AlarmNames) > 0 {
		cw := cloudwatch.NewFromConfig(awsCfg)
		out, err := cw.DescribeAlarms(ctx, &cloudwatch.DescribeAlarmsInput{AlarmNames: cfg.AlarmNames})
		if err != nil {
			oc.Notes = append(oc.Notes, "describe alarms failed: "+err.Error())
		} else {
			for _, a := range out.MetricAlarms {
				oc.AlarmStates[aws.ToString(a.AlarmName)] = string(a.StateValue)
			}
		}
	}

	sq := sqs.NewFromConfig(awsCfg)
	oc.QueueDepth, oc.QueueInFlight = queueDepth(ctx, sq, cfg.QueueURL)
	oc.DLQDepth, _ = queueDepth(ctx, sq, cfg.DLQURL)

	es := ecs.NewFromConfig(awsCfg)
	if svc, err := es.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster: &cfg.Cluster, Services: []string{cfg.Service},
	}); err == nil && len(svc.Services) > 0 {
		s := svc.Services[0]
		roll := "n/a"
		if len(s.Deployments) > 0 {
			roll = string(s.Deployments[0].RolloutState)
		}
		oc.Deployment = fmt.Sprintf("rollout=%s running=%d/%d deployments=%d",
			roll, s.RunningCount, s.DesiredCount, len(s.Deployments))
	} else if err != nil {
		oc.Notes = append(oc.Notes, "describe services failed: "+err.Error())
	}

	oc.RecentErrors = recentErrors(ctx, cloudwatchlogs.NewFromConfig(awsCfg), cfg.LogGroup)
	return oc
}

func queueDepth(ctx context.Context, sq *sqs.Client, url string) (visible, inFlight string) {
	if url == "" {
		return "n/a", "n/a"
	}
	out, err := sq.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: &url,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameApproximateNumberOfMessages,
			sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		return "err", "err"
	}
	return out.Attributes["ApproximateNumberOfMessages"], out.Attributes["ApproximateNumberOfMessagesNotVisible"]
}

// recentErrors 抓最近 15 分钟含 error/failed 的日志片段，最多 20 条。
func recentErrors(ctx context.Context, cl *cloudwatchlogs.Client, group string) []string {
	start := time.Now().Add(-15 * time.Minute).UnixMilli()
	pat := "?ERROR ?error ?failed ?panic"
	limit := int32(20)
	out, err := cl.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  &group,
		StartTime:     &start,
		FilterPattern: &pat,
		Limit:         &limit,
	})
	if err != nil {
		return []string{"filter logs failed: " + err.Error()}
	}
	var lines []string
	for _, e := range out.Events {
		lines = append(lines, truncate(aws.ToString(e.Message), 300))
	}
	return lines
}

// diagnose 调通义千问 qwen-plus，要求返回结构化 JSON 诊断。
func diagnose(ctx context.Context, cfg config, key string, oc opsContext) (diagnosis, error) {
	snapshot, _ := json.MarshalIndent(oc, "", "  ")
	system := "你是错题本项目(mistake，Go+ECS Fargate+RDS+SNS/SQS/DLQ+CloudWatch)的资深 SRE。" +
		"根据运维快照做诊断，只输出一个 JSON 对象，字段：" +
		"severity(info|warning|high|critical)、summary(一句话)、rootCause、suggestions(字符串数组，可执行建议)、security(布尔，是否疑似安全问题)。" +
		"不要输出 JSON 以外的任何内容。"
	body := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": "运维快照：\n" + string(snapshot)},
		},
		"temperature": 0.2,
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, dashscopeURL, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return diagnosis{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return diagnosis{}, fmt.Errorf("dashscope %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &chat); err != nil || len(chat.Choices) == 0 {
		return diagnosis{}, fmt.Errorf("bad dashscope response: %s", truncate(string(data), 300))
	}
	var diag diagnosis
	if err := json.Unmarshal([]byte(stripFences(chat.Choices[0].Message.Content)), &diag); err != nil {
		return diagnosis{}, fmt.Errorf("parse diagnosis: %w", err)
	}
	if diag.Severity == "" {
		diag.Severity = "warning"
	}
	return diag, nil
}

// shouldFileTicket：告警触发一律建单；主动巡检仅在严重或有告警时建单。
func shouldFileTicket(trigger string, diag diagnosis, oc opsContext) bool {
	if trigger == "alarm" {
		return true
	}
	if oc.anyAlarming() {
		return true
	}
	return diag.Severity == "high" || diag.Severity == "critical"
}

// ---- GitHub 工单 ----

// fingerprint 用于去重：同一告警 / 同一巡检窗口只留一个 open issue。
func fingerprint(trigger, alarmName string) string {
	if trigger == "alarm" && alarmName != "" {
		return "alarm:" + alarmName
	}
	return "scan:" + time.Now().UTC().Format("2006-01-02")
}

func findOpenIssue(ctx context.Context, repo, token, fp string) (string, bool) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues?state=open&labels=aiops&per_page=100", repo)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	ghAuth(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var issues []struct {
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&issues)
	for _, is := range issues {
		if strings.Contains(is.Title, "["+fp+"]") {
			return is.HTMLURL, true
		}
	}
	return "", false
}

func createIssue(ctx context.Context, repo, token, fp string, diag diagnosis, oc opsContext) (string, error) {
	title := fmt.Sprintf("[AIOps][%s] %s", fp, truncate(diag.Summary, 80))
	var b strings.Builder
	fmt.Fprintf(&b, "**严重级别**：%s　**疑似安全问题**：%t\n\n", diag.Severity, diag.Security)
	fmt.Fprintf(&b, "**触发**：%s", oc.Trigger)
	if oc.AlarmName != "" {
		fmt.Fprintf(&b, "（告警 `%s`）", oc.AlarmName)
	}
	fmt.Fprintf(&b, "\n\n**根因推测**\n%s\n\n**修复建议**\n", diag.RootCause)
	for _, s := range diag.Suggestions {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	snap, _ := json.MarshalIndent(oc, "", "  ")
	fmt.Fprintf(&b, "\n<details><summary>运维快照</summary>\n\n```json\n%s\n```\n</details>\n\n", string(snap))
	b.WriteString("_由 mistake-aiops-agent 自动生成，仅供参考，不代表已自动处置。_")

	payload, _ := json.Marshal(map[string]any{
		"title":  title,
		"body":   b.String(),
		"labels": []string{"aiops", "severity:" + diag.Severity},
	})
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues", repo)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	ghAuth(req, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(data, &created)
	return created.HTMLURL, nil
}

func ghAuth(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// ---- 小工具 ----

func getParam(ctx context.Context, awsCfg aws.Config, name string) (string, error) {
	out, err := ssm.NewFromConfig(awsCfg).GetParameter(ctx, &ssm.GetParameterInput{
		Name: &name, WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.Parameter.Value), nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// stripFences 去掉模型偶尔包裹的 ```json ... ``` 代码块。
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func main() {
	lambda.Start(handler)
}
