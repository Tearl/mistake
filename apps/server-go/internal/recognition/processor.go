package recognition

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"mistakeserver/internal/ai"
	"mistakeserver/internal/db"
	"mistakeserver/internal/storage"
)

type Processor struct {
	Q     *db.Queries
	AI    *ai.Client
	Store storage.Storage
}

var ErrAlreadyProcessing = errors.New("recognition job is already processing")

func New(q *db.Queries, client *ai.Client, store storage.Storage) *Processor {
	return &Processor{Q: q, AI: client, Store: store}
}

func ParseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := id.Scan(value)
	return id, err
}

// Process claims and completes one job. Repeated delivery of an already completed
// job is treated as success so the duplicate can be deleted. A current processing
// lease is left in the queue; a stale lease can be reclaimed by ClaimRecognitionJob.
func (p *Processor) Process(ctx context.Context, id pgtype.UUID, userID string, attempt int32) error {
	job, err := p.Q.ClaimRecognitionJob(ctx, db.ClaimRecognitionJobParams{
		ID: id, Attempts: attempt, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, getErr := p.Q.GetRecognitionJob(ctx, db.GetRecognitionJobParams{ID: id, UserID: userID})
		if getErr != nil {
			return getErr
		}
		if current.Status == "succeeded" {
			return nil
		}
		if current.Status == "processing" {
			return ErrAlreadyProcessing
		}
		return errors.New("recognition job is not processable")
	}
	if err != nil {
		return err
	}

	image, err := p.Store.Get(ctx, job.ImageFileID)
	if err != nil {
		return err
	}
	result, err := p.AI.Recognize(ctx, image, mimeFromPath(job.ImageFileID))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = p.Q.CompleteRecognitionJob(ctx, db.CompleteRecognitionJobParams{ID: id, Result: raw})
	return err
}

func mimeFromPath(value string) string {
	ext := strings.ToLower(filepath.Ext(value))
	switch ext {
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
