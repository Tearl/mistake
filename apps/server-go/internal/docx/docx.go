// Package docx 手写一个最小的 .docx 生成器（archive/zip + WordprocessingML），
// 零第三方依赖。只覆盖标题 + 段落，等价于原云函数 exportDocx 的输出内容。
package docx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

type Item struct {
	Subject         string
	KnowledgePoints []string
	Difficulty      string
	QuestionType    string
	OcrText         string
	Answer          string
	ErrorReason     string
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

const rels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

// Build 生成一份 Word 文档字节流。title 为清单标题，subtitle 为副行（如「共 N 题…」）。
func Build(title, subtitle string, items []Item) ([]byte, error) {
	var body strings.Builder
	body.WriteString(heading(title, 1))
	if subtitle != "" {
		body.WriteString(para(subtitle))
	}
	body.WriteString(para(""))

	for i, it := range items {
		body.WriteString(heading(fmt.Sprintf("第 %d 题", i+1), 2))

		var meta []string
		if it.Subject != "" {
			meta = append(meta, "学科："+it.Subject)
		}
		if len(it.KnowledgePoints) > 0 {
			meta = append(meta, "知识点："+strings.Join(it.KnowledgePoints, "、"))
		}
		if it.Difficulty != "" {
			meta = append(meta, "难度："+it.Difficulty)
		}
		if it.QuestionType != "" {
			meta = append(meta, "题型："+it.QuestionType)
		}
		if len(meta) > 0 {
			body.WriteString(para(strings.Join(meta, "   |   ")))
		}

		body.WriteString(block("题干：", orText(it.OcrText, "（图片题，无文字）")))
		if it.Answer != "" {
			body.WriteString(block("答案：", it.Answer))
		}
		if it.ErrorReason != "" {
			body.WriteString(block("易错点：", it.ErrorReason))
		}
		body.WriteString(para(""))
	}

	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() +
		`<w:sectPr/></w:body></w:document>`

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	files := []struct{ name, content string }{
		{"[Content_Types].xml", contentTypes},
		{"_rels/.rels", rels},
		{"word/document.xml", document},
	}
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// 一个「标签加粗 + 多行正文」的块（含换行拆段，避免挤成一行）
func block(label, text string) string {
	var b strings.Builder
	b.WriteString(paraBold(label))
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		b.WriteString(para(ln))
	}
	return b.String()
}

func para(text string) string {
	return "<w:p><w:r><w:t xml:space=\"preserve\">" + esc(text) + "</w:t></w:r></w:p>"
}

func paraBold(text string) string {
	return "<w:p><w:r><w:rPr><w:b/></w:rPr><w:t xml:space=\"preserve\">" + esc(text) + "</w:t></w:r></w:p>"
}

func heading(text string, level int) string {
	sz := "32"
	if level == 2 {
		sz = "28"
	}
	return "<w:p><w:pPr><w:spacing w:before=\"120\" w:after=\"60\"/></w:pPr>" +
		"<w:r><w:rPr><w:b/><w:sz w:val=\"" + sz + "\"/></w:rPr>" +
		"<w:t xml:space=\"preserve\">" + esc(text) + "</w:t></w:r></w:p>"
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}

func orText(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
