package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"mistakeserver/internal/db"
	"mistakeserver/internal/docx"
)

// POST /api/export  body {subject, ids}  -> 直接流式返回 .docx 下载
func (s *Server) Export(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Subject string   `json:"subject"`
		Ids     []string `json:"ids"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if b.Ids == nil {
		b.Ids = []string{}
	}
	rows, err := s.Q.ExportMistakes(r.Context(), db.ExportMistakesParams{
		UserID: s.user(), Ids: b.Ids, Subject: b.Subject,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusNotFound, "empty")
		return
	}

	items := make([]docx.Item, 0, len(rows))
	for _, m := range rows {
		items = append(items, docx.Item{
			Subject:         m.Subject,
			KnowledgePoints: m.KnowledgePoints,
			Difficulty:      m.Difficulty,
			QuestionType:    m.QuestionType,
			OcrText:         m.OcrText,
			Answer:          m.Answer,
			ErrorReason:     m.ErrorReason,
		})
	}

	title := subjectOr(b.Subject) + "错题清单"
	exportedAt := time.Now().Add(8 * time.Hour).UTC().Format("2006-01-02 15:04")
	subtitle := fmt.Sprintf("共 %d 题 · 导出于 %s", len(items), exportedAt)

	buf, err := docx.Build(title, subtitle, items)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	filename := "mistakes-" + strconv.FormatInt(time.Now().Unix(), 10) + ".docx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(buf)))
	_, _ = w.Write(buf)
}

func subjectOr(s string) string {
	if s == "" {
		return "全部"
	}
	return s
}
