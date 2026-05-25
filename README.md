# FrameFleet

FrameFleet 是一个分布式视频处理实验项目：把视频拆成多个时间片，并行做轮廓处理，最后合成透明背景 GIF。

当前真实链路已经跑通：Entry 控制面、WorkerGo 调度/传输、C++ engine 的 split/process/assemble，以及 4-worker 真实视频 smoke。

## 当前状态

已实现：

- Entry Server：Go + Gin 控制面。
- WorkerGo：真实 worker agent，负责注册、心跳、扫描本地输入、HTTP 收发、slot 管理。
- C++ engine：WorkerGo 管理的本地 subprocess，通过 JSON Lines 协议通信。
- 视频处理：
  - `split_video`: `ffprobe` + `ffmpeg` 拆分 mp4。
  - `process_segment`: OpenCV Canny，输出红色轮廓透明 PNG 帧流。
  - `assemble_gif`: FFAF artifact -> PNG frames -> ffmpeg 透明 GIF。
- 一阶段 artifact 使用单文件 FFAF v1，Go 不解析其内容。
- 真实单机 smoke 和真实 4-worker cluster smoke。

暂未实现或仍需生产化：

- 持久化数据库。
- task 超时和重试。
- notification retry。
- 认证鉴权。
- 中间产物/tmp GC。
- 更完整的 slot 子进程死亡重拉、请求重试、隔离治理。

详细架构见：

- `AGENTS.md`
- `docs/architecture-distilled.md`
- `docs/future-work.md`
- `worker-node/protocol/ffaf-v1.md`

## 目录结构

```text
entry-server/                    # Entry 控制面
  cmd/server/main.go             # Entry 启动入口
  internal/handlers/             # Gin handlers
  internal/service/              # 业务逻辑和内存状态
  internal/model/                # Entry 内部状态模型
  internal/server/               # Gin server、路由、中间件
  internal/logger/               # slog 日志封装

worker-node/
  go/cmd/worker-agent/           # 真实 WorkerGo agent
  go/internal/                   # WorkerGo 内部模块
  cmd/test-worker/               # 简单 register/heartbeat 测试客户端
  cpp/                           # C++ engine
  protocol/                      # Go/C++ 示例协议和 FFAF 文档

pkg/protocol/                    # Entry/Worker 共享 HTTP 协议 DTO
scripts/                         # smoke 测试脚本
docs/                            # 架构和 future work 文档
```

## 依赖

Go：

- Go 1.22+

C++ engine：

- CMake 3.16+
- C++17 compiler
- `ffmpeg`
- `ffprobe`
- OpenCV 4 development package

Ubuntu/WSL 示例：

```bash
sudo apt install cmake g++ ffmpeg libopencv-dev
```

检查：

```bash
go version
ffmpeg -version
ffprobe -version
pkg-config --modversion opencv4
```

## 初始化

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go mod tidy
```

构建 C++ engine：

```bash
cmake -S worker-node/cpp -B worker-node/cpp/build
cmake --build worker-node/cpp/build
```

默认 engine binary 路径：

```text
worker-node/cpp/build/framefleet-engine
```

## 启动 Entry

```bash
source ~/.zshrc
ENTRY_SERVER_ADDR=127.0.0.1:18080 \
LOG_LEVEL=info \
LOG_OUTPUT=stdout \
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./entry-server/cmd/server
```

Entry split policy 默认：

```text
SPLIT_TARGET_SEGMENT_DURATION_MS=3000
SPLIT_TARGET_SEGMENT_SIZE_BYTES=0
SPLIT_MAX_SEGMENTS=8
```

## 启动真实 Worker

推荐用项目内模板生成器创建项目外 Worker 运行目录。它会生成
`worker.env`、数据目录、日志目录，以及 `run.sh`/`stop.sh`/`status.sh`/`logs.sh`：

```bash
deploy/worker-template/init-worker.sh \
  --name worker-19001 \
  --root /home/ckw/framefleet-workers \
  --port 19001 \
  --entry http://127.0.0.1:18080 \
  --advertised 127.0.0.1:19001 \
  --slots 2
```

运行：

```bash
/home/ckw/framefleet-workers/worker-19001/run.sh
/home/ckw/framefleet-workers/worker-19001/status.sh
/home/ckw/framefleet-workers/worker-19001/logs.sh -f
/home/ckw/framefleet-workers/worker-19001/stop.sh
```

模板默认让 WorkerGo 日志写到该实例的 `logs/worker-agent.log`，不在标准输出刷业务日志。模板详情见
`deploy/worker-template/README.md`。

也可以手工启动：

```bash
source ~/.zshrc
WORKER_LISTEN_ADDR=:19001 \
WORKER_ADVERTISED_ADDRESS=127.0.0.1:19001 \
ENTRY_BASE_URL=http://127.0.0.1:18080 \
WORKER_TOTAL_SLOTS=1 \
WORKER_DATA_DIR=/tmp/framefleet-worker1/data \
WORKER_INPUT_DIR=/tmp/framefleet-worker1/input \
WORKER_ENGINE_BINARY=worker-node/cpp/build/framefleet-engine \
WORKER_CANNY_LOW_THRESHOLD=180 \
WORKER_CANNY_HIGH_THRESHOLD=360 \
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./worker-node/go/cmd/worker-agent
```

把 `.mp4` 放进 `WORKER_INPUT_DIR` 后，WorkerGo 会扫描并注册 job。

## 测试

Go 全量测试：

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go test ./...
```

C++ artifact 测试：

```bash
cmake --build worker-node/cpp/build
ctest --test-dir worker-node/cpp/build --output-on-failure
```

CPP 真实本地链路测试：

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod \
go test ./worker-node/go/internal/enginepool -run TestCppEngineRealVideoPipeline -count=1
```

Entry 生命周期 smoke：

```bash
source ~/.zshrc
scripts/smoke_task_lifecycle.sh
```

真实单机 smoke：

```bash
source ~/.zshrc
KEEP_LOGS=1 bash scripts/smoke_worker_single_real.sh
```

真实 4-worker cluster smoke：

```bash
source ~/.zshrc
KEEP_LOGS=1 bash scripts/smoke_worker_cluster_real.sh
```

`smoke_worker_cluster_real.sh` 会启动 Entry + 4 workers，并处理 6 个真实视频任务。它比单元测试慢，通常只在明确需要验证全链路时运行。

旧的 `scripts/smoke_worker_cluster.sh` 仍是 fake/压力风格 cluster smoke，不适合验证真实视频处理质量。

## 主要配置

Entry：

```text
ENTRY_SERVER_ADDR=:8080
WORKER_HEARTBEAT_TIMEOUT_SECONDS=30
WORKER_HEARTBEAT_CHECK_INTERVAL_SECONDS=10
SPLIT_TARGET_SEGMENT_DURATION_MS=3000
SPLIT_TARGET_SEGMENT_SIZE_BYTES=0
SPLIT_MAX_SEGMENTS=8
```

Worker：

```text
WORKER_LISTEN_ADDR=:9001
ENTRY_BASE_URL=http://127.0.0.1:8080
WORKER_ADVERTISED_ADDRESS=127.0.0.1:9001
WORKER_TOTAL_SLOTS=4
WORKER_DATA_DIR=worker-node/data
WORKER_INPUT_DIR=worker-node/data/input
WORKER_ENGINE_BINARY=worker-node/cpp/build/framefleet-engine
WORKER_CANNY_LOW_THRESHOLD=180
WORKER_CANNY_HIGH_THRESHOLD=360
```

日志：

```text
LOG_OUTPUT=stdout|file|both|discard
LOG_FILE=logs/entry-server.log
WORKER_LOG_OUTPUT=stdout|file|both|discard
WORKER_LOG_FILE=logs/worker-agent.log
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

Entry -> Worker：

```http
POST /tasks/assemble_gif
POST /jobs/result
```

Worker -> Worker:

```http
POST /segments/:task_id/upload
GET  /artifacts/:task_id
GET  /results/:result_name
```

公共 HTTP 协议结构定义在 `pkg/protocol/`。

## 设计原则

- Entry 只做控制面，不代理视频、artifact 或 GIF bytes。
- Worker 之间直接传输 segment 和 artifact。
- internal job 也必须先注册到 Entry。
- internal/external 是 task 级别属性，不是 job 级别属性。
- job 幂等 key 是 `source_worker_address + video_name`。
- Go/CPP 协议不包含 job/task 概念；C++ 只知道 op 和文件路径。
- Worker artifact 对 Go 不透明；C++ 使用 FFAF v1 读写 artifact。
- 公共 HTTP 协议结构放 `pkg/protocol`，不要在 Entry 和 Worker 两边重复定义。
