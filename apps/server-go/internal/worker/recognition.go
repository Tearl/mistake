package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"mistakeserver/internal/db"
	"mistakeserver/internal/messaging"
	"mistakeserver/internal/recognition"
)

type sqsAPI interface {
	ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

type RecognitionWorker struct {
	client            sqsAPI
	queueURL          string
	waitTimeSeconds   int32
	visibilityTimeout int32
	maxReceiveCount   int32
	processor         *recognition.Processor
	q                 *db.Queries
}

func NewRecognitionWorker(
	ctx context.Context,
	region, queueURL string,
	waitTimeSeconds, visibilityTimeout, maxReceiveCount int32,
	processor *recognition.Processor,
	q *db.Queries,
) (*RecognitionWorker, error) {
	if queueURL == "" {
		return nil, errors.New("SQS_QUEUE_URL is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &RecognitionWorker{
		client:            sqs.NewFromConfig(cfg),
		queueURL:          queueURL,
		waitTimeSeconds:   waitTimeSeconds,
		visibilityTimeout: visibilityTimeout,
		maxReceiveCount:   maxReceiveCount,
		processor:         processor,
		q:                 q,
	}, nil
}

func (w *RecognitionWorker) Run(ctx context.Context) error {
	log.Printf("recognition worker started queue=%s", w.queueURL)
	for ctx.Err() == nil {
		out, err := w.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            &w.queueURL,
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     w.waitTimeSeconds,
			VisibilityTimeout:   w.visibilityTimeout,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{
				types.MessageSystemAttributeNameApproximateReceiveCount,
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("event=sqs_receive_failed error=%q", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}
		for _, message := range out.Messages {
			w.handle(ctx, message)
		}
	}
	return nil
}

func (w *RecognitionWorker) handle(ctx context.Context, message types.Message) {
	attempt := receiveCount(message)
	var event messaging.Event
	if message.Body == nil || json.Unmarshal([]byte(*message.Body), &event) != nil || event.Validate() != nil {
		log.Printf("event=message_rejected messageId=%q receiveCount=%d", value(message.MessageId), attempt)
		return // poison message: leave it for SQS redrive policy
	}

	jobID, err := recognition.ParseUUID(event.Payload.JobID)
	if err != nil {
		log.Printf("event=message_rejected messageId=%q jobId=%q error=%q", value(message.MessageId), event.Payload.JobID, err)
		return
	}

	err = w.processor.Process(ctx, jobID, event.Payload.UserID, attempt)
	if errors.Is(err, recognition.ErrAlreadyProcessing) {
		log.Printf("event=recognition_already_processing messageId=%q jobId=%q receiveCount=%d",
			value(message.MessageId), event.Payload.JobID, attempt)
		return
	}
	if err != nil {
		status := "retrying"
		if attempt >= w.maxReceiveCount {
			status = "dead_lettered"
		}
		_, markErr := w.q.MarkRecognitionJobError(ctx, db.MarkRecognitionJobErrorParams{
			ID: jobID, Status: status, Attempts: attempt, LastError: truncate(err.Error(), 1000),
		})
		log.Printf("event=recognition_failed messageId=%q jobId=%q receiveCount=%d status=%s error=%q markError=%q",
			value(message.MessageId), event.Payload.JobID, attempt, status, err, markErr)
		return
	}

	if message.ReceiptHandle == nil {
		log.Printf("event=message_delete_skipped jobId=%q reason=missing_receipt_handle", event.Payload.JobID)
		return
	}
	_, err = w.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl: &w.queueURL, ReceiptHandle: message.ReceiptHandle,
	})
	if err != nil {
		log.Printf("event=message_delete_failed jobId=%q error=%q", event.Payload.JobID, err)
		return
	}
	log.Printf("event=recognition_succeeded messageId=%q jobId=%q receiveCount=%d",
		value(message.MessageId), event.Payload.JobID, attempt)
}

func receiveCount(message types.Message) int32 {
	value := message.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)]
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 1 {
		return 1
	}
	return int32(n)
}

func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
