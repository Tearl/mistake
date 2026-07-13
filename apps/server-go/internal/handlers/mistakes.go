package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"mistakeserver/internal/db"
)

// GET /api/mistakes?subject=&limit=
func (s *Server) ListMistakes(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	limit := int32(100)
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = int32(n)
		}
	}
	rows, err := s.Q.ListMistakes(r.Context(), db.ListMistakesParams{
		UserID: s.user(), Subject: subject, Lim: limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDTOs(rows))
}

// GET /api/mistakes/{id}
func (s *Server) GetMistake(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	m, err := s.Q.GetMistake(r.Context(), db.GetMistakeParams{UserID: s.user(), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDTO(m))
}

type createMistakeBody struct {
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
}

// POST /api/mistakes
func (s *Server) CreateMistake(w http.ResponseWriter, r *http.Request) {
	var b createMistakeBody
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if b.KnowledgePoints == nil {
		b.KnowledgePoints = []string{}
	}
	if b.Difficulty == "" {
		b.Difficulty = "中"
	}
	if b.Mastery == "" {
		b.Mastery = "unmastered"
	}
	if b.Kind == "" {
		b.Kind = "photo"
	}
	m, err := s.Q.CreateMistake(r.Context(), db.CreateMistakeParams{
		UserID:          s.user(),
		ImageFileID:     b.ImageFileID,
		Subject:         b.Subject,
		KnowledgePoints: b.KnowledgePoints,
		QuestionType:    b.QuestionType,
		Difficulty:      b.Difficulty,
		OcrText:         b.OcrText,
		Answer:          b.Answer,
		ErrorReason:     b.ErrorReason,
		Mastery:         b.Mastery,
		WrongCount:      b.WrongCount,
		Kind:            b.Kind,
		FromMistakeID:   b.FromMistakeID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDTO(m))
}

// PATCH /api/mistakes/{id}  body {mastery}
func (s *Server) UpdateMastery(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var b struct {
		Mastery string `json:"mastery"`
	}
	if err := decode(r, &b); err != nil || b.Mastery == "" {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	m, err := s.Q.UpdateMastery(r.Context(), db.UpdateMasteryParams{
		UserID: s.user(), Mastery: b.Mastery, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDTO(m))
}

// 三档反馈 -> 掌握状态
var gradeToMastery = map[string]string{
	"unknown":  "unmastered",
	"fuzzy":    "reviewing",
	"mastered": "mastered",
}

// POST /api/mistakes/{id}/grade  body {action}
func (s *Server) GradeMistake(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var b struct {
		Action string `json:"action"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	mastery, ok := gradeToMastery[b.Action]
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid action")
		return
	}
	inc := int32(0)
	if b.Action == "unknown" {
		inc = 1
	}
	m, err := s.Q.GradeMistake(r.Context(), db.GradeMistakeParams{
		UserID: s.user(), Mastery: mastery, WrongInc: inc, ID: id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 记录一次复习，供统计页本周柱状图 / 连续天数使用（失败不影响主流程）
	_ = s.Q.InsertReviewLog(r.Context(), db.InsertReviewLogParams{
		UserID: s.user(), MistakeID: uuidStr(id), Action: b.Action,
	})
	writeJSON(w, http.StatusOK, toDTO(m))
}

// DELETE /api/mistakes/{id}
func (s *Server) DeleteMistake(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	imageFileID, err := s.Q.DeleteMistake(r.Context(), db.DeleteMistakeParams{UserID: s.user(), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 顺带删掉图片对象（忽略错误）
	if imageFileID != "" {
		_ = s.Store.Delete(r.Context(), imageFileID)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// GET /api/random?subject=&mastery=
func (s *Server) Random(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	mastery := r.URL.Query().Get("mastery")
	if mastery == "" {
		mastery = "all"
	}
	pool, err := s.Q.CountPool(r.Context(), db.CountPoolParams{
		UserID: s.user(), Subject: subject, Mastery: mastery,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pool == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"data": nil, "poolCount": 0})
		return
	}
	m, err := s.Q.RandomMistake(r.Context(), db.RandomMistakeParams{
		UserID: s.user(), Subject: subject, Mastery: mastery,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": toDTO(m), "poolCount": pool})
}
