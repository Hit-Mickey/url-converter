# --- 第一阶段：编译 ---
FROM golang:1.26-alpine AS builder

# 安装构建依赖
RUN apk add --no-cache git

WORKDIR /app

# 处理依赖关系
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o converter .

# --- 第二阶段：运行 ---
FROM alpine:latest

# 安装必要的基础库
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/converter .

COPY images ./images

# 预创建核心存储目录
RUN mkdir -p /app/core

# 暴露端口
EXPOSE 5000

# 启动程序
ENTRYPOINT ["./converter"]