// 作业二演示 Lambda：普通调用型接口，被 ECS 经 Cloud Map 发现后用 lambda:Invoke 调用。
package main

import (
	"context"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
)

type Response struct {
	Message string `json:"message"`
	Service string `json:"service"`
	Time    string `json:"time"`
}

func handle(ctx context.Context, _ map[string]any) (Response, error) {
	return Response{
		Message: "hello from lambda, discovered via AWS Cloud Map",
		Service: "mistake-cloudmap-hello",
		Time:    time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func main() { lambda.Start(handle) }
