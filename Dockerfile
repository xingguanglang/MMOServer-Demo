# --- build stage ---
FROM golang:1.26 AS build
WORKDIR /src

# 先拷依赖清单并下载,利用 Docker 层缓存(代码变了也不用重新下依赖)。
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 只编译服务器(不含 ebiten 客户端,因此无需 X11/GL 依赖)。
# CGO_ENABLED=0 生成静态二进制,可放进极简运行镜像。
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

# --- run stage ---
FROM alpine:3.20
COPY --from=build /out/server /server
EXPOSE 9000
ENTRYPOINT ["/server"]
