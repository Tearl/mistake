# 多阶段构建：编出静态二进制，再塞进空镜像
# GO_IMAGE 可覆盖：国内网络可传 --build-arg GO_IMAGE=m.daocloud.io/docker.io/library/golang:1.26
ARG GO_IMAGE=golang:1.26
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 迁移文件通过 go:embed 打进二进制；CGO 关掉出纯静态二进制
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server .

# scratch 空镜像：本地即有、无需拉取，绕开 gcr.io/docker.io 的墙。
# 静态二进制不依赖 libc；只需补上 CA 证书（调 DashScope / AWS 走 HTTPS）。
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /server /server
EXPOSE 3000
ENTRYPOINT ["/server"]
