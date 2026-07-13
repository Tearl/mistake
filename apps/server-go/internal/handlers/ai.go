package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"mistakeserver/internal/ai"
)

// POST /api/upload  (multipart form, field "file") -> {imageFileID}
func (s *Server) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil { // 20MB
		writeErr(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}
	name := randomHex(12) + ext

	imageFileID, err := s.Store.Put(r.Context(), name, mimeFromExt(ext), file)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"imageFileID": imageFileID})
}

// POST /api/recognize  body {imageFileID}
func (s *Server) Recognize(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ImageFileID string `json:"imageFileID"`
	}
	if err := decode(r, &b); err != nil || b.ImageFileID == "" {
		writeErr(w, http.StatusBadRequest, "missing imageFileID")
		return
	}
	img, err := s.Store.Get(r.Context(), b.ImageFileID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.AI.Recognize(r.Context(), img, mimeFromExt(filepath.Ext(b.ImageFileID)))
	if errors.Is(err, ai.ErrNoKey) {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

// POST /api/similar  body: 原题字段 + count + exclude
func (s *Server) Similar(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Subject         string   `json:"subject"`
		KnowledgePoints []string `json:"knowledgePoints"`
		QuestionType    string   `json:"questionType"`
		Difficulty      string   `json:"difficulty"`
		OcrText         string   `json:"ocrText"`
		Answer          string   `json:"answer"`
		Count           int      `json:"count"`
		Exclude         []string `json:"exclude"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	items, err := s.AI.Similar(r.Context(), ai.SimilarInput{
		Subject:         b.Subject,
		KnowledgePoints: b.KnowledgePoints,
		QuestionType:    b.QuestionType,
		Difficulty:      b.Difficulty,
		OcrText:         b.OcrText,
		Answer:          b.Answer,
		Count:           b.Count,
		Exclude:         b.Exclude,
	})
	if errors.Is(err, ai.ErrNoKey) {
		writeErr(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
