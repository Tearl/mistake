package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/servicediscovery"
)

// 作业二：ECS 后端通过 AWS Cloud Map 发现并调用 Lambda。
// HTTP 命名空间 + DiscoverInstances：注册实例时把 Lambda 函数名写进 functionName 属性；
// 这里发现后取出函数名再 lambda:Invoke，全程不硬编码 Lambda 地址。

var (
	awsOnce      sync.Once
	sdClient     *servicediscovery.Client
	lambdaClient *lambda.Client
	awsErr       error
)

func awsClients(ctx context.Context) error {
	awsOnce.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			awsErr = err
			return
		}
		sdClient = servicediscovery.NewFromConfig(cfg)
		lambdaClient = lambda.NewFromConfig(cfg)
	})
	return awsErr
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// GET /api/cloudmap-hello
func (s *Server) CloudMapHello(w http.ResponseWriter, r *http.Request) {
	ns := envOr("CLOUDMAP_NAMESPACE", "mistake-services")
	svc := envOr("CLOUDMAP_SERVICE", "hello")

	if err := awsClients(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "aws config: "+err.Error())
		return
	}

	// 1. 通过 Cloud Map 发现服务实例
	out, err := sdClient.DiscoverInstances(r.Context(), &servicediscovery.DiscoverInstancesInput{
		NamespaceName: &ns,
		ServiceName:   &svc,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cloud map discover: "+err.Error())
		return
	}
	if len(out.Instances) == 0 {
		writeErr(w, http.StatusServiceUnavailable, "no instances registered in cloud map")
		return
	}
	fn := out.Instances[0].Attributes["functionName"]
	if fn == "" {
		writeErr(w, http.StatusServiceUnavailable, "discovered instance has no functionName attribute")
		return
	}

	// 2. 调用发现到的 Lambda
	inv, err := lambdaClient.Invoke(r.Context(), &lambda.InvokeInput{FunctionName: &fn})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "invoke lambda: "+err.Error())
		return
	}
	if inv.FunctionError != nil {
		writeErr(w, http.StatusBadGateway, "lambda error: "+*inv.FunctionError)
		return
	}

	var lambdaJSON any
	_ = json.Unmarshal(inv.Payload, &lambdaJSON)

	writeJSON(w, http.StatusOK, map[string]any{
		"via":                "aws-cloud-map",
		"namespace":          ns,
		"service":            svc,
		"discoveredFunction": fn,
		"instanceCount":      len(out.Instances),
		"lambdaResponse":     lambdaJSON,
	})
}
