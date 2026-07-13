# 多阶段构建：编出静态二进制，再塞进极小的 distroless 镜像
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 迁移文件通过 go:embed 打进二进制；CGO 关掉出纯静态二进制
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server .

# distroless static 自带 CA 证书（调 DashScope / AWS 需要 HTTPS）
FROM gcr.io/distroless/static-debian12
COPY --from=build /server /server
EXPOSE 3000
ENTRYPOINT ["/server"]
