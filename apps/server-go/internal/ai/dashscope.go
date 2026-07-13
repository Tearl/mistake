// Package ai 封装通义千问（DashScope OpenAI 兼容接口）的调用。
// 逻辑移植自小程序云函数 recognizeMistake / generateSimilar。
package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiURL       = "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
	visionModel  = "qwen-vl-plus"
	textModel    = "qwen-plus"
	recognizePmt = `你是一个错题识别助手。请仔细看这张题目图片，识别并分析它。
只输出一个 JSON 对象，不要任何多余文字，也不要用 markdown 代码块包裹，格式如下：
{
  "subject": "学科，如 数学/语文/英语/物理 等",
  "knowledgePoints": ["知识点1", "知识点2"],
  "questionType": "题型，如 选择题/填空题/解答题",
  "difficulty": "易 或 中 或 难",
  "ocrText": "题干的完整文字",
  "answer": "这道题的参考答案或解题思路；如果图片里没有答案，就根据题目推断给出",
  "errorReason": "学生做这类题常见的易错点 / 可能的错误原因，一句话"
}`
)

// ErrNoKey 表示未配置 DASHSCOPE_API_KEY（与原云函数一致，返回明确错误）
var ErrNoKey = errors.New("missing DASHSCOPE_API_KEY env")

type Client struct {
	key  string
	http *http.Client
}

func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) HasKey() bool { return c.key != "" }

// ---- 识别错题图片 ----

type RecognizeResult struct {
	Subject         string   `json:"subject"`
	KnowledgePoints []string `json:"knowledgePoints"`
	QuestionType    string   `json:"questionType"`
	Difficulty      string   `json:"difficulty"`
	OcrText         string   `json:"ocrText"`
	Answer          string   `json:"answer"`
	ErrorReason     string   `json:"errorReason"`
}

func (c *Client) Recognize(ctx context.Context, image []byte, mimeType string) (*RecognizeResult, error) {
	if c.key == "" {
		return nil, ErrNoKey
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image)

	body := map[string]any{
		"model": visionModel,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
					map[string]any{"type": "text", "text": recognizePmt},
				},
			},
		},
	}
	text, err := c.chat(ctx, body)
	if err != nil {
		return nil, err
	}
	obj := extractJSON(text, '{', '}')
	if obj == "" {
		return nil, fmt.Errorf("parse failed: %s", truncate(text, 200))
	}
	var d RecognizeResult
	if err := json.Unmarshal([]byte(obj), &d); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}
	if d.Difficulty == "" {
		d.Difficulty = "中"
	}
	if d.KnowledgePoints == nil {
		d.KnowledgePoints = []string{}
	}
	return &d, nil
}

// ---- 举一反三 ----

type SimilarInput struct {
	Subject         string
	KnowledgePoints []string
	QuestionType    string
	Difficulty      string
	OcrText         string
	Answer          string
	Count           int
	Exclude         []string
}

type SimilarItem struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Analysis string `json:"analysis"`
}

func (c *Client) Similar(ctx context.Context, in SimilarInput) ([]SimilarItem, error) {
	if c.key == "" {
		return nil, ErrNoKey
	}
	n := in.Count
	if n <= 0 {
		n = 1
	}
	kp := strings.Join(in.KnowledgePoints, "、")
	avoidBlock := ""
	var avoid []string
	for _, q := range in.Exclude {
		if strings.TrimSpace(q) != "" {
			avoid = append(avoid, q)
		}
	}
	if len(avoid) > 0 {
		var b strings.Builder
		b.WriteString("\n请不要与下面这些已经生成过的变式题重复或雷同：\n")
		for i, q := range avoid {
			fmt.Fprintf(&b, "%d. %s\n", i+1, q)
		}
		avoidBlock = b.String()
	}

	prompt := fmt.Sprintf(`你是一位资深%s老师。下面是学生做错的一道题，请出 %d 道"举一反三"的变式题：
- 考查相同知识点（%s），难度与原题相当（%s）
- 题目情境、数据、设问方式要和原题不同，不要简单换数字
- 解析简明扼要，控制在 1-2 句话
原题题型：%s
原题题干：%s
原题答案：%s
%s

只输出一个 JSON 数组，不要任何多余文字，也不要用 markdown 代码块包裹，格式：
[
  { "question": "变式题题干", "answer": "答案", "analysis": "解题思路/解析" }
]`,
		in.Subject, n, orDefault(kp, "与原题一致"), orDefault(in.Difficulty, "中"),
		in.QuestionType, orDefault(in.OcrText, "（仅有图片，无文字）"), orDefault(in.Answer, "（无）"), avoidBlock)

	body := map[string]any{
		"model":      textModel,
		"messages":   []any{map[string]any{"role": "user", "content": prompt}},
		"max_tokens": 1500,
	}
	text, err := c.chat(ctx, body)
	if err != nil {
		return nil, err
	}
	arrStr := extractJSON(text, '[', ']')
	if arrStr == "" {
		return nil, fmt.Errorf("parse failed: %s", truncate(text, 200))
	}
	var arr []SimilarItem
	if err := json.Unmarshal([]byte(arrStr), &arr); err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}
	out := make([]SimilarItem, 0, len(arr))
	for _, x := range arr {
		if strings.TrimSpace(x.Question) != "" {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("parse failed: empty")
	}
	return out, nil
}

// ---- 底层 HTTP + 解析 ----

func (c *Client) chat(ctx context.Context, body map[string]any) (string, error) {
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dashscope %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				// content 可能是字符串，也可能是数组（多模态）；用 RawMessage 兼容
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("no choices in response")
	}
	return contentToText(parsed.Choices[0].Message.Content), nil
}

// content 兼容 "字符串" 或 [{type,text}] 两种形态
func contentToText(rm json.RawMessage) string {
	if len(rm) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(rm, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(rm, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// 去掉 markdown 代码块包裹，截取首尾括号之间的内容
func extractJSON(text string, open, close byte) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "```json", ""), "```", ""))
	start := strings.IndexByte(cleaned, open)
	end := strings.LastIndexByte(cleaned, close)
	if start == -1 || end == -1 || end < start {
		return ""
	}
	return cleaned[start : end+1]
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
