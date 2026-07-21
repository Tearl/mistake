package main

import (
	"context"
	"fmt"
	"log"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"mistakeserver/internal/config"
	"mistakeserver/internal/perf"
)

// isS3 表示生产模式：性能数据走 CloudWatch + S3；否则走本地文件（开发）。
func isS3(cfg *config.Config) bool { return cfg.Storage == "s3" }

// newOpsStore 构造 /api/ops/summary 的读取源与清洗任务的写入源。
func newOpsStore(ctx context.Context, cfg *config.Config) (perf.SummaryStore, error) {
	if !isS3(cfg) {
		return perf.NewLocalStore(cfg.OpsLocalDir), nil
	}
	aws, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	return perf.NewS3Store(s3.NewFromConfig(aws), cfg.S3Bucket, cfg.OpsS3Key), nil
}

// newPerfSink 构造性能 SDK 的批量落地目标。
func newPerfSink(ctx context.Context, cfg *config.Config) (perf.Sink, error) {
	if !isS3(cfg) {
		return perf.NewFileSink(cfg.PerfLocalDir), nil
	}
	aws, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	return perf.NewCloudWatchSink(cloudwatchlogs.NewFromConfig(aws), cfg.PerfLogGroup), nil
}

// newPerfSource 构造清洗任务的事件来源。
func newPerfSource(ctx context.Context, cfg *config.Config) (perf.Source, error) {
	if !isS3(cfg) {
		return perf.NewFileSource(cfg.PerfLocalDir), nil
	}
	aws, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	return perf.NewCloudWatchSource(cloudwatchlogs.NewFromConfig(aws), cfg.PerfLogGroup), nil
}

// runPerfAggregation 是 APP_MODE=perfagg 的入口：清洗一次窗口数据并退出。
func runPerfAggregation(ctx context.Context, cfg *config.Config) error {
	src, err := newPerfSource(ctx, cfg)
	if err != nil {
		return fmt.Errorf("perf source: %w", err)
	}
	store, err := newOpsStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("ops store: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	sum, err := perf.RunAggregation(runCtx, src, store, cfg.OpsWindowMinutes)
	if err != nil {
		return err
	}
	log.Printf("event=perf_aggregated window=%dm requests=%d errors=%d p95=%dms routes=%d",
		cfg.OpsWindowMinutes, sum.TotalRequests, sum.TotalErrors, sum.OverallP95, len(sum.Routes))
	return nil
}
