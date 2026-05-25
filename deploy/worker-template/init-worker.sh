#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  deploy/worker-template/init-worker.sh --name NAME --root DIR --port PORT --entry URL --advertised HOST:PORT [options]

Required:
  --port PORT                  Local WorkerGo listen port.
  --entry URL                  Entry base URL, including scheme, for example http://127.0.0.1:18080.

Common:
  --name NAME                  Worker instance directory name. Defaults to worker-PORT.
  --root DIR                   Parent runtime directory. Defaults to /tmp/framefleet-workers.
  --advertised HOST:PORT       Address registered with Entry. Defaults to 127.0.0.1:PORT.
  --slots N                    Worker slot count. Defaults to 2.
  --engine PATH                C++ engine binary. Defaults to REPO/worker-node/cpp/build/framefleet-engine.
  --repo DIR                   FrameFleet repo path. Defaults to the current git root.
  --heartbeat-seconds N        Heartbeat interval. Defaults to 10.
  --scan-seconds N             Source input scan interval. Defaults to 10.
  --canny-low N                Canny low threshold. Defaults to 80.
  --canny-high N               Canny high threshold. Defaults to 160.
  --force                      Overwrite an existing worker directory.
  -h, --help                   Show this help.

Generated scripts:
  run.sh                       Start worker in background.
  stop.sh                      Stop worker.
  status.sh                    Show pid, port, health, and recent snapshot.
  logs.sh [-f] [LINES]         Read or follow worker log file.
EOF
}

repo_dir=""
root_dir="/tmp/framefleet-workers"
name=""
port=""
entry_url=""
advertised=""
slots="2"
engine_path=""
heartbeat_seconds="10"
scan_seconds="10"
canny_low="80"
canny_high="160"
force="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo_dir="${2:?missing value for --repo}"
      shift 2
      ;;
    --root)
      root_dir="${2:?missing value for --root}"
      shift 2
      ;;
    --name)
      name="${2:?missing value for --name}"
      shift 2
      ;;
    --port)
      port="${2:?missing value for --port}"
      shift 2
      ;;
    --entry)
      entry_url="${2:?missing value for --entry}"
      shift 2
      ;;
    --advertised)
      advertised="${2:?missing value for --advertised}"
      shift 2
      ;;
    --slots)
      slots="${2:?missing value for --slots}"
      shift 2
      ;;
    --engine)
      engine_path="${2:?missing value for --engine}"
      shift 2
      ;;
    --heartbeat-seconds)
      heartbeat_seconds="${2:?missing value for --heartbeat-seconds}"
      shift 2
      ;;
    --scan-seconds)
      scan_seconds="${2:?missing value for --scan-seconds}"
      shift 2
      ;;
    --canny-low)
      canny_low="${2:?missing value for --canny-low}"
      shift 2
      ;;
    --canny-high)
      canny_high="${2:?missing value for --canny-high}"
      shift 2
      ;;
    --force)
      force="1"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$port" || -z "$entry_url" ]]; then
  echo "--port and --entry are required" >&2
  usage >&2
  exit 2
fi

if ! [[ "$port" =~ ^[0-9]+$ ]]; then
  echo "--port must be numeric: $port" >&2
  exit 2
fi
if ! [[ "$slots" =~ ^[0-9]+$ ]] || [[ "$slots" -lt 1 ]]; then
  echo "--slots must be a positive integer: $slots" >&2
  exit 2
fi
if [[ "$entry_url" != http://* && "$entry_url" != https://* ]]; then
  echo "--entry must include http:// or https://: $entry_url" >&2
  exit 2
fi

if [[ -z "$repo_dir" ]]; then
  repo_dir="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
fi
repo_dir="$(cd "$repo_dir" && pwd)"

if [[ -z "$name" ]]; then
  name="worker-${port}"
fi
if [[ -z "$advertised" ]]; then
  advertised="127.0.0.1:${port}"
fi
if [[ -z "$engine_path" ]]; then
  engine_path="${repo_dir}/worker-node/cpp/build/framefleet-engine"
fi

worker_dir="${root_dir%/}/${name}"
data_dir="${worker_dir}/data"
input_dir="${worker_dir}/input"
logs_dir="${worker_dir}/logs"
pid_file="${worker_dir}/worker-agent.pid"
worker_log="${logs_dir}/worker-agent.log"
launcher_log="${logs_dir}/launcher.log"

if [[ -e "$worker_dir" && "$force" != "1" ]]; then
  echo "worker directory already exists: $worker_dir" >&2
  echo "pass --force to overwrite generated files" >&2
  exit 1
fi

mkdir -p \
  "$input_dir" \
  "$logs_dir" \
  "$data_dir/spool/uploads" \
  "$data_dir/spool/outgoing" \
  "$data_dir/spool/artifacts" \
  "$data_dir/spool/results" \
  "$data_dir/spool/tmp"

cat >"${worker_dir}/worker.env" <<EOF
WORKER_LISTEN_ADDR=:${port}
WORKER_ADVERTISED_ADDRESS=${advertised}
ENTRY_BASE_URL=${entry_url}
WORKER_TOTAL_SLOTS=${slots}

WORKER_DATA_DIR=${data_dir}
WORKER_INPUT_DIR=${input_dir}
WORKER_ENGINE_BINARY=${engine_path}

WORKER_LOG_LEVEL=info
WORKER_LOG_OUTPUT=file
WORKER_LOG_FILE=${worker_log}

WORKER_HEARTBEAT_INTERVAL_SECONDS=${heartbeat_seconds}
WORKER_SOURCE_SCAN_INTERVAL_SECONDS=${scan_seconds}
WORKER_DISK_TOTAL_BYTES=1000000000
WORKER_DISK_FREE_BYTES=800000000

WORKER_CANNY_LOW_THRESHOLD=${canny_low}
WORKER_CANNY_HIGH_THRESHOLD=${canny_high}
EOF

cat >"${worker_dir}/run.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail

WORKER_DIR=${worker_dir@Q}
REPO_DIR=${repo_dir@Q}
ENV_FILE="\$WORKER_DIR/worker.env"
PID_FILE=${pid_file@Q}
LAUNCHER_LOG=${launcher_log@Q}
PORT=${port}

mkdir -p "\$WORKER_DIR/logs"

if [[ -f "\$PID_FILE" ]]; then
  pid="\$(cat "\$PID_FILE")"
  if [[ -n "\$pid" ]] && kill -0 "\$pid" 2>/dev/null; then
    echo "${name} already running: pid=\$pid"
    echo "worker log: ${worker_log}"
    exit 0
  fi
  rm -f "\$PID_FILE"
fi

port_pid="\$(ss -ltnp "sport = :\$PORT" 2>/dev/null | sed -n 's/.*pid=\([0-9]\+\).*/\1/p' | head -n1 || true)"
if [[ -n "\$port_pid" ]]; then
  echo "\$port_pid" > "\$PID_FILE"
  echo "${name} already running on port \$PORT: pid=\$port_pid"
  echo "worker log: ${worker_log}"
  exit 0
fi

setsid bash -lc "cd '\$REPO_DIR' && exec env WORKER_ENV_FILE='\$ENV_FILE' GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go run ./worker-node/go/cmd/worker-agent" >>"\$LAUNCHER_LOG" 2>&1 &
pid=\$!
echo "\$pid" > "\$PID_FILE"
echo "${name} started: pid=\$pid"
echo "worker log: ${worker_log}"
echo "launcher log: \$LAUNCHER_LOG"
EOF

cat >"${worker_dir}/stop.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail

PID_FILE=${pid_file@Q}
PORT=${port}

pid=""
if [[ -f "\$PID_FILE" ]]; then
  pid="\$(cat "\$PID_FILE")"
fi

if [[ -z "\$pid" ]] || ! kill -0 "\$pid" 2>/dev/null; then
  pid="\$(ss -ltnp "sport = :\$PORT" 2>/dev/null | sed -n 's/.*pid=\([0-9]\+\).*/\1/p' | head -n1 || true)"
fi

if [[ -z "\$pid" ]] || ! kill -0 "\$pid" 2>/dev/null; then
  rm -f "\$PID_FILE"
  echo "${name} is not running"
  exit 0
fi

kill -- "-\$pid" 2>/dev/null || kill "\$pid" 2>/dev/null || true
for _ in {1..30}; do
  if ! kill -0 "\$pid" 2>/dev/null; then
    rm -f "\$PID_FILE"
    echo "${name} stopped"
    exit 0
  fi
  sleep 0.2
done

kill -9 -- "-\$pid" 2>/dev/null || kill -9 "\$pid" 2>/dev/null || true
rm -f "\$PID_FILE"
echo "${name} killed after timeout"
EOF

cat >"${worker_dir}/status.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail

PID_FILE=${pid_file@Q}
PORT=${port}
ADVERTISED=${advertised@Q}
ENTRY=${entry_url@Q}
WORKER_LOG=${worker_log@Q}

pid=""
if [[ -f "\$PID_FILE" ]]; then
  pid="\$(cat "\$PID_FILE")"
fi
if [[ -z "\$pid" ]] || ! kill -0 "\$pid" 2>/dev/null; then
  pid="\$(ss -ltnp "sport = :\$PORT" 2>/dev/null | sed -n 's/.*pid=\([0-9]\+\).*/\1/p' | head -n1 || true)"
fi

echo "name: ${name}"
echo "entry: \$ENTRY"
echo "advertised: \$ADVERTISED"
echo "port: \$PORT"
if [[ -n "\$pid" ]] && kill -0 "\$pid" 2>/dev/null; then
  echo "process: running pid=\$pid"
else
  echo "process: stopped"
fi

if command -v curl >/dev/null 2>&1; then
  if curl -fsS --max-time 2 "http://127.0.0.1:\$PORT/healthz" >/dev/null 2>&1; then
    echo "healthz: ok"
  else
    echo "healthz: failed"
  fi
fi

if [[ -f "\$WORKER_LOG" ]]; then
  echo "latest runtime snapshot:"
  grep '"event":"worker_runtime_snapshot"' "\$WORKER_LOG" | tail -n 1 || true
else
  echo "worker log missing: \$WORKER_LOG"
fi
EOF

cat >"${worker_dir}/logs.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail

WORKER_LOG=${worker_log@Q}
LINES=120
FOLLOW=0

while [[ \$# -gt 0 ]]; do
  case "\$1" in
    -f|--follow)
      FOLLOW=1
      shift
      ;;
    -n|--lines)
      LINES="\${2:?missing value for \$1}"
      shift 2
      ;;
    [0-9]*)
      LINES="\$1"
      shift
      ;;
    -h|--help)
      echo "Usage: logs.sh [-f|--follow] [-n LINES]"
      exit 0
      ;;
    *)
      echo "unknown argument: \$1" >&2
      exit 2
      ;;
  esac
done

if [[ "\$FOLLOW" == "1" ]]; then
  exec tail -n "\$LINES" -f "\$WORKER_LOG"
fi
exec tail -n "\$LINES" "\$WORKER_LOG"
EOF

chmod +x \
  "${worker_dir}/run.sh" \
  "${worker_dir}/stop.sh" \
  "${worker_dir}/status.sh" \
  "${worker_dir}/logs.sh"

echo "worker runtime created: $worker_dir"
echo "edit env: ${worker_dir}/worker.env"
echo "start: ${worker_dir}/run.sh"
echo "status: ${worker_dir}/status.sh"
echo "logs: ${worker_dir}/logs.sh -f"
