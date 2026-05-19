# FrameFleet

FrameFleet 是 FramePipeline 的分布式控制面重构版本，目标是把视频切成多个时间片并行处理，最后合成 GIF。

当前阶段只实现分布式控制面，不迁移旧 C++ 视频处理逻辑。Entry Server 使用 Go + Gin 编写，Worker 端目前只有一个用于注册和心跳的测试客户端。

## 当前状态

Entry Server 已实现：

- worker 注册
- worker 心跳
- internal/external job 创建
- job 幂等：`source_worker_address + video_name`
- segment task 分配
- segment task accepted/completed/failed
- external job 的 assemble task 调度协议
- final assembled 回报
- source worker best-effort 结果通知协议
- 按来源地址和视频名查询 job/result
- JSON 结构化日志
- smoke 测试脚本

暂未实现：

- 真实 Worker HTTP 服务
- segment 上传、处理、artifact 下载
- GIF 实际生成
- GORM/数据库持久化
- task 超时和重试
- 认证鉴权

详细架构见：

- `docs/architecture-distilled.md`
- `docs/future-work.md`
- `AGENTS.md`

## 目录结构

```text
entry-server/              # Entry Server 控制面
  cmd/server/main.go       # Entry 启动入口
  internal/handlers/       # Gin handlers
  internal/service/        # 业务逻辑和内存状态
  internal/model/          # Entry 内部状态模型
  internal/server/         # Gin server、路由、中间件
  internal/logger/         # slog 日志封装

worker-node/
  cmd/test-worker/         # 简单测试 worker：注册 + 心跳

pkg/protocol/              # Entry/Worker 共享 HTTP 协议 DTO
scripts/                   # smoke 测试脚本
docs/                      # 架构和 future work 文档
```

## 安装 Go 环境

如果机器还没有 Go，可以安装 Go 1.22.x。

```bash
cd /tmp
wget https://mirrors.aliyun.com/golang/go1.22.12.linux-amd64.tar.gz

sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz
```

配置 PATH：

```bash
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.zshrc
source ~/.zshrc
```

如果使用 bash：

```bash
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
```

配置 Go 模块镜像：

```bash
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=sum.golang.google.cn
```

验证：

```bash
go version
go env GOPROXY
```

## 初始化依赖

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go mod tidy
```

这里把 Go build cache 和 module cache 放到 `/tmp`，是为了避免某些沙箱或受限环境不能写 home 目录缓存。

## 启动 Entry Server

默认监听 `:8080`：

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./entry-server/cmd/server
```

指定地址：

```bash
ENTRY_SERVER_ADDR=127.0.0.1:18080 \
LOG_LEVEL=info \
LOG_OUTPUT=stdout \
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./entry-server/cmd/server
```

## 启动测试 Worker

测试 worker 只负责向 Entry 注册并维持心跳，不处理真实视频。

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./worker-node/cmd/test-worker \
  -entry http://127.0.0.1:18080 \
  -port 19001 \
  -slots 4
```

开多个 worker 时改端口：

```bash
go run ./worker-node/cmd/test-worker -entry http://127.0.0.1:18080 -port 19002 -slots 2
go run ./worker-node/cmd/test-worker -entry http://127.0.0.1:18080 -port 19003 -slots 2
```

常用参数：

```text
-entry http://127.0.0.1:8080    Entry Server 地址
-host 127.0.0.1                 worker 对外声明 host
-port 9001                      worker 对外声明 port
-slots 4                        worker 最大 slot 数
-disk-total 1000000000          磁盘总空间，字节
-disk-free 800000000            磁盘可用空间，字节
-heartbeat-interval 10s         心跳间隔
```

## 运行测试

编译级检查：

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go test ./...
```

端到端 smoke 测试：

```bash
source ~/.zshrc
scripts/smoke_task_lifecycle.sh
```

smoke 脚本会临时启动 Entry Server，并用 HTTP 调用覆盖：

- worker register
- heartbeat
- external job
- job duplicate / `already_exists`
- task accepted
- task completed
- task failed
- slot reservation/release
- internal job
- assembled 回报
- result 查询

## 主要环境变量

Entry Server：

```text
ENTRY_SERVER_ADDR=:8080
WORKER_HEARTBEAT_TIMEOUT_SECONDS=30
WORKER_HEARTBEAT_CHECK_INTERVAL_SECONDS=10
```

日志：

```text
LOG_LEVEL=info
LOG_OUTPUT=stdout
LOG_FILE=logs/entry-server.log
```

`LOG_OUTPUT` 可选：

```text
stdout
file
both
discard
```

## 主要接口

Worker：

```http
POST /workers/register
POST /workers/heartbeat
```

Job：

```http
POST /jobs
GET  /jobs/result?address=127.0.0.1:9001&video_name=demo.mp4
POST /jobs/:job_id/assembled
```

Task：

```http
POST /tasks/:task_id/accepted
POST /tasks/:task_id/completed
POST /tasks/:task_id/failed
```

Entry -> Worker 协议：

```http
POST /tasks/assemble_gif
POST /jobs/result
```

这些跨进程协议的请求体和响应体定义在 `pkg/protocol/`。

## 设计原则

- Entry 只做控制面，不传视频、不转发 artifact、不代理 GIF。
- Worker 之间直接传输 segment 和 artifact。
- internal job 也必须先注册到 Entry。
- job 幂等 key 是 `source_worker_address + video_name`。
- 公共协议结构放 `pkg/protocol`，不要在 Entry 和 Worker 两边重复定义。
- Entry 内部状态放 `entry-server/internal/model`。
- 不要在持有一个 manager 锁时调用另一个 manager 的 helper。
- 不要持锁进行 HTTP 请求。

## 后续开发

继续开发前建议先看：

```text
AGENTS.md
docs/future-work.md
docs/architecture-distilled.md
```

如果新增协议或修改接口，请同步更新：

- `pkg/protocol`
- `docs/architecture-distilled.md`
- `scripts/smoke_task_lifecycle.sh`

如果只是记录暂缓事项或迁移注意，请更新：

- `docs/future-work.md`
