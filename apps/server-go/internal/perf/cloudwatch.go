package perf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// cwSink 把批量事件写进 CloudWatch Logs 专用日志组。
type cwSink struct {
	client   *cloudwatchlogs.Client
	group    string
	stream   string
	ensured  bool
}

// NewCloudWatchSink 生产用 Sink：日志组已由基建预建，流按实例创建。
func NewCloudWatchSink(client *cloudwatchlogs.Client, group string) Sink {
	host, _ := os.Hostname()
	if host == "" {
		host = "task"
	}
	return &cwSink{
		client: client, group: group,
		stream: fmt.Sprintf("%s-%d", host, time.Now().UnixNano()),
	}
}

func (c *cwSink) ensure(ctx context.Context) error {
	if c.ensured {
		return nil
	}
	_, err := c.client.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName: &c.group, LogStreamName: &c.stream,
	})
	var exists *cwtypes.ResourceAlreadyExistsException
	if err != nil && !errors.As(err, &exists) {
		return err
	}
	c.ensured = true
	return nil
}

func (c *cwSink) Write(ctx context.Context, events []Event) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	logEvents := make([]cwtypes.InputLogEvent, 0, len(events))
	for _, e := range events {
		raw, _ := json.Marshal(e)
		logEvents = append(logEvents, cwtypes.InputLogEvent{
			Message:   aws.String(string(raw)),
			Timestamp: aws.Int64(e.TS),
		})
	}
	// PutLogEvents 现已不要求 sequence token。
	_, err := c.client.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName:  &c.group,
		LogStreamName: &c.stream,
		LogEvents:     logEvents,
	})
	return err
}

// cwSource 供清洗任务从日志组读回窗口内事件。
type cwSource struct {
	client *cloudwatchlogs.Client
	group  string
}

func NewCloudWatchSource(client *cloudwatchlogs.Client, group string) Source {
	return &cwSource{client: client, group: group}
}

func (c *cwSource) Fetch(ctx context.Context, start, end int64) ([]Event, error) {
	var out []Event
	var token *string
	for {
		resp, err := c.client.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName: &c.group,
			StartTime:    aws.Int64(start),
			EndTime:      aws.Int64(end),
			NextToken:    token,
		})
		if err != nil {
			return nil, err
		}
		for _, e := range resp.Events {
			var ev Event
			if json.Unmarshal([]byte(aws.ToString(e.Message)), &ev) == nil {
				out = append(out, ev)
			}
		}
		if resp.NextToken == nil {
			return out, nil
		}
		token = resp.NextToken
	}
}
