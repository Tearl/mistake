package messaging

import (
	"context"
	"encoding/json"
	"errors"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
)

type Publisher interface {
	Publish(context.Context, Event) error
}

type snsAPI interface {
	Publish(context.Context, *sns.PublishInput, ...func(*sns.Options)) (*sns.PublishOutput, error)
}

type SNSPublisher struct {
	client   snsAPI
	topicARN string
}

func NewSNSPublisher(ctx context.Context, region, topicARN string) (*SNSPublisher, error) {
	if topicARN == "" {
		return nil, errors.New("SNS_TOPIC_ARN is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &SNSPublisher{client: sns.NewFromConfig(cfg), topicARN: topicARN}, nil
}

func (p *SNSPublisher) Publish(ctx context.Context, event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	dataType := "String"
	eventType := event.EventType
	_, err = p.client.Publish(ctx, &sns.PublishInput{
		TopicArn: &p.topicARN,
		Message:  stringPtr(string(body)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"eventType": {DataType: &dataType, StringValue: &eventType},
		},
	})
	return err
}

func stringPtr(v string) *string { return &v }
