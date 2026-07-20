package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"mistakeserver/internal/ai"
	"mistakeserver/internal/db"
	"mistakeserver/internal/messaging"
)

type recognitionJobDTO struct {
	ID          string              `json:"jobId"`
	RequestID   string              `json:"requestId"`
	ImageFileID string              `json:"imageFileID"`
	Status      string              `json:"status"`
	Attempts    int32               `json:"attempts"`
	Result      *ai.RecognizeResult `json:"result,omitempty"`
	LastError   string              `json:"lastError,omitempty"`
	CreatedAt   int64               `json:"createdAt"`
	CompletedAt int64               `json:"completedAt,omitempty"`
}

func recognitionDTO(job db.RecognitionJob) recognitionJobDTO {
	var result *ai.RecognizeResult
	if len(job.Result) > 0 && string(job.Result) != "{}" {
		var parsed ai.RecognizeResult
		if json.Unmarshal(job.Result, &parsed) == nil {
			result = &parsed
		}
	}
	return recognitionJobDTO{
		ID: uuidStr(job.ID), RequestID: job.RequestID, ImageFileID: job.ImageFileID,
		Status: job.Status, Attempts: job.Attempts, Result: result, LastError: job.LastError,
		CreatedAt: tsMillis(job.CreatedAt), CompletedAt: tsMillis(job.CompletedAt),
	}
}

// POST /api/recognitions creates an idempotent recognition job. Production
// publishes to SNS; local development processes the same job synchronously.
func (s *Server) CreateRecognition(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ImageFileID string `json:"imageFileID"`
		RequestID   string `json:"requestId"`
	}
	if err := decode(r, &body); err != nil || body.ImageFileID == "" {
		writeErr(w, http.StatusBadRequest, "imageFileID is required")
		return
	}
	if body.RequestID == "" {
		body.RequestID = uuidStr(newUUID())
	}

	eventID := newUUID()
	job, err := s.Q.CreateRecognitionJob(r.Context(), db.CreateRecognitionJobParams{
		RequestID: body.RequestID, EventID: eventID, UserID: s.user(), ImageFileID: body.ImageFileID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job.Status == "succeeded" || job.Status == "failed" {
		writeJSON(w, http.StatusOK, recognitionDTO(job))
		return
	}

	if s.Cfg.AsyncRecognition {
		if s.Publisher == nil {
			_ = s.Q.MarkRecognitionJobPublishFailed(r.Context(), db.MarkRecognitionJobPublishFailedParams{
				ID: job.ID, LastError: "async recognition publisher is unavailable",
			})
			writeErr(w, http.StatusServiceUnavailable, "async recognition publisher is unavailable")
			return
		}
		if !job.PublishedAt.Valid {
			event := messaging.NewRecognitionRequested(
				uuidStr(job.EventID), uuidStr(job.ID), job.UserID, job.ImageFileID,
			)
			if err := s.Publisher.Publish(r.Context(), event); err != nil {
				_ = s.Q.MarkRecognitionJobPublishFailed(r.Context(), db.MarkRecognitionJobPublishFailedParams{
					ID: job.ID, LastError: err.Error(),
				})
				writeErr(w, http.StatusServiceUnavailable, "failed to queue recognition job")
				return
			}
			_ = s.Q.MarkRecognitionJobPublished(r.Context(), job.ID)
		}
		writeJSON(w, http.StatusAccepted, recognitionDTO(job))
		return
	}

	if err := s.Recognition.Process(r.Context(), job.ID, job.UserID, 1); err != nil {
		failed, markErr := s.Q.MarkRecognitionJobError(r.Context(), db.MarkRecognitionJobErrorParams{
			ID: job.ID, Status: "failed", Attempts: 1, LastError: err.Error(),
		})
		if markErr == nil {
			writeJSON(w, http.StatusOK, recognitionDTO(failed))
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	job, err = s.Q.GetRecognitionJob(r.Context(), db.GetRecognitionJobParams{ID: job.ID, UserID: job.UserID})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, recognitionDTO(job))
}

// GET /api/recognitions/{id}
func (s *Server) GetRecognition(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	job, err := s.Q.GetRecognitionJob(r.Context(), db.GetRecognitionJobParams{ID: id, UserID: s.user()})
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, recognitionDTO(job))
}
