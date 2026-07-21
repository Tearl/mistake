package handlers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"mistakeserver/internal/ai"
	"mistakeserver/internal/config"
	"mistakeserver/internal/db"
	"mistakeserver/internal/messaging"
	"mistakeserver/internal/perf"
	"mistakeserver/internal/recognition"
	"mistakeserver/internal/storage"
)

type Server struct {
	Cfg         *config.Config
	Pool        *pgxpool.Pool
	Q           *db.Queries
	AI          *ai.Client
	Store       storage.Storage
	Publisher   messaging.Publisher
	Recognition *recognition.Processor
	OpsStore    perf.SummaryStore // 性能清洗产物的读取源（/api/ops/summary）
}

func New(
	cfg *config.Config,
	pool *pgxpool.Pool,
	aiClient *ai.Client,
	store storage.Storage,
	publisher messaging.Publisher,
	processor *recognition.Processor,
	opsStore perf.SummaryStore,
) *Server {
	return &Server{
		Cfg: cfg, Pool: pool, Q: db.New(pool), AI: aiClient, Store: store,
		Publisher: publisher, Recognition: processor, OpsStore: opsStore,
	}
}

// user 返回当前请求的用户 id（单用户模式恒为 dev 用户）
func (s *Server) user() string { return config.DevUserID }

// ---- DTO：JSON 字段名与前端 Mistake 类型一致 ----

type MistakeDTO struct {
	ID              string   `json:"_id"`
	ImageFileID     string   `json:"imageFileID"`
	Subject         string   `json:"subject"`
	KnowledgePoints []string `json:"knowledgePoints"`
	QuestionType    string   `json:"questionType"`
	Difficulty      string   `json:"difficulty"`
	OcrText         string   `json:"ocrText"`
	Answer          string   `json:"answer"`
	ErrorReason     string   `json:"errorReason"`
	Mastery         string   `json:"mastery"`
	WrongCount      int32    `json:"wrongCount"`
	Kind            string   `json:"kind"`
	FromMistakeID   string   `json:"fromMistakeId"`
	CreatedAt       int64    `json:"createdAt"`
}

func toDTO(m db.Mistake) MistakeDTO {
	kp := m.KnowledgePoints
	if kp == nil {
		kp = []string{}
	}
	return MistakeDTO{
		ID:              uuidStr(m.ID),
		ImageFileID:     m.ImageFileID,
		Subject:         m.Subject,
		KnowledgePoints: kp,
		QuestionType:    m.QuestionType,
		Difficulty:      m.Difficulty,
		OcrText:         m.OcrText,
		Answer:          m.Answer,
		ErrorReason:     m.ErrorReason,
		Mastery:         m.Mastery,
		WrongCount:      m.WrongCount,
		Kind:            m.Kind,
		FromMistakeID:   m.FromMistakeID,
		CreatedAt:       tsMillis(m.CreatedAt),
	}
}

func toDTOs(ms []db.Mistake) []MistakeDTO {
	out := make([]MistakeDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, toDTO(m))
	}
	return out
}

// ---- 类型转换辅助 ----

func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	err := u.Scan(s)
	return u, err
}

func newUUID() pgtype.UUID {
	var id pgtype.UUID
	_, _ = rand.Read(id.Bytes[:])
	id.Bytes[6] = (id.Bytes[6] & 0x0f) | 0x40
	id.Bytes[8] = (id.Bytes[8] & 0x3f) | 0x80
	id.Valid = true
	return id
}

func tsMillis(t pgtype.Timestamptz) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.UnixMilli()
}

func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// ---- HTTP 辅助 ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
