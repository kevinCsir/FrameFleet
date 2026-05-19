#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENTRY_ADDR="${ENTRY_ADDR:-127.0.0.1:18082}"
ENTRY_URL="http://${ENTRY_ADDR}"
WORKER1_ADDR="${WORKER1_ADDR:-127.0.0.1:19111}"
WORKER2_ADDR="${WORKER2_ADDR:-127.0.0.1:19112}"
WORKER3_ADDR="${WORKER3_ADDR:-127.0.0.1:19113}"
ENGINE_BINARY="${ENGINE_BINARY:-${ROOT_DIR}/worker-node/cpp/build/framefleet-engine}"

TMP_DIR="$(mktemp -d)"
ENTRY_LOG="${TMP_DIR}/entry.log"
WORKER1_LOG="${TMP_DIR}/worker1.log"
WORKER2_LOG="${TMP_DIR}/worker2.log"
WORKER3_LOG="${TMP_DIR}/worker3.log"
PIDS=()

cleanup() {
  for pid in "${PIDS[@]}"; do
    if kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
    fi
  done
  for pid in "${PIDS[@]}"; do
    wait "${pid}" 2>/dev/null || true
  done
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

json_get() {
  local expr="$1"
  python3 -c 'import json,sys
try:
    data=json.load(sys.stdin)
    value=data
    for part in sys.argv[1].split("."):
        if part == "":
            continue
        if part.isdigit():
            value=value[int(part)]
        else:
            value=value[part]
    if value is None:
        print("")
    else:
        print(value)
except Exception:
    sys.exit(1)' "${expr}"
}

wait_for_http() {
  local url="$1"
  local name="$2"
  local log_path="$3"
  for _ in $(seq 1 120); do
    if curl -sS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "${name} did not become ready" >&2
  echo "${name} log:" >&2
  cat "${log_path}" >&2 || true
  return 1
}

query_result() {
  local video_name="$1"
  curl -fsS "${ENTRY_URL}/jobs/result?address=${WORKER1_ADDR}&video_name=${video_name}"
}

wait_for_result() {
  local video_name="$1"
  local response=""
  for _ in $(seq 1 160); do
    if response="$(query_result "${video_name}" 2>/dev/null)"; then
      local status
      status="$(printf '%s' "${response}" | json_get status || true)"
      local job_status
      job_status="$(printf '%s' "${response}" | json_get job_status || true)"
      if [[ "${status}" == "success" && "${job_status}" == "completed" ]]; then
        printf '%s' "${response}"
        return 0
      fi
      if [[ "${job_status}" == "failed" ]]; then
        echo "job ${video_name} failed: ${response}" >&2
        echo "entry log:" >&2
        tail -n 200 "${ENTRY_LOG}" >&2 || true
        echo "worker1 log:" >&2
        tail -n 200 "${WORKER1_LOG}" >&2 || true
        echo "worker2 log:" >&2
        tail -n 200 "${WORKER2_LOG}" >&2 || true
        echo "worker3 log:" >&2
        tail -n 200 "${WORKER3_LOG}" >&2 || true
        return 1
      fi
    fi
    sleep 0.5
  done
  echo "timed out waiting for result of ${video_name}" >&2
  echo "last response: ${response}" >&2
  echo "entry log:" >&2
  tail -n 200 "${ENTRY_LOG}" >&2 || true
  echo "worker1 log:" >&2
  tail -n 200 "${WORKER1_LOG}" >&2 || true
  echo "worker2 log:" >&2
  tail -n 200 "${WORKER2_LOG}" >&2 || true
  echo "worker3 log:" >&2
  tail -n 200 "${WORKER3_LOG}" >&2 || true
  return 1
}

start_entry() {
  ENTRY_SERVER_ADDR="${ENTRY_ADDR}" \
  LOG_OUTPUT=discard \
  SPLIT_MAX_SEGMENTS=2 \
  GOCACHE=/tmp/go-build \
  GOMODCACHE=/tmp/go/pkg/mod \
  go run ./entry-server/cmd/server >"${ENTRY_LOG}" 2>&1 &
  PIDS+=("$!")
}

start_worker() {
  local index="$1"
  local listen_addr="$2"
  local advertised_addr="$3"
  local data_dir="$4"
  local input_dir="$5"
  local log_path="$6"

  WORKER_LISTEN_ADDR="${listen_addr}" \
  WORKER_ADVERTISED_ADDRESS="${advertised_addr}" \
  ENTRY_BASE_URL="${ENTRY_URL}" \
  WORKER_TOTAL_SLOTS=1 \
  WORKER_DATA_DIR="${data_dir}" \
  WORKER_INPUT_DIR="${input_dir}" \
  WORKER_SOURCE_SCAN_INTERVAL_SECONDS=1 \
  WORKER_HEARTBEAT_INTERVAL_SECONDS=1 \
  WORKER_ENGINE_BINARY="${ENGINE_BINARY}" \
  GIN_MODE=release \
  GOCACHE=/tmp/go-build \
  GOMODCACHE=/tmp/go/pkg/mod \
  go run ./worker-node/go/cmd/worker-agent >"${log_path}" 2>&1 &
  PIDS+=("$!")
  echo "started worker ${index} at ${advertised_addr}"
}

cd "${ROOT_DIR}"

if [[ ! -x "${ENGINE_BINARY}" ]]; then
  echo "engine binary not found at ${ENGINE_BINARY}" >&2
  echo "build it with: cmake -S worker-node/cpp -B worker-node/cpp/build && cmake --build worker-node/cpp/build" >&2
  exit 1
fi

mkdir -p "${TMP_DIR}/worker1/input" "${TMP_DIR}/worker2/input" "${TMP_DIR}/worker3/input"
for i in $(seq 0 9); do
  printf 'framefleet-video-%02d:abcdefghijklmnopqrstuvwxyz:%02d\n' "${i}" "${i}" >"${TMP_DIR}/worker1/input/video_${i}.mp4"
done

start_entry
wait_for_http "${ENTRY_URL}/jobs/result?address=probe&video_name=probe" "entry" "${ENTRY_LOG}"

start_worker 2 ":19112" "${WORKER2_ADDR}" "${TMP_DIR}/worker2/data" "${TMP_DIR}/worker2/input" "${WORKER2_LOG}"
start_worker 3 ":19113" "${WORKER3_ADDR}" "${TMP_DIR}/worker3/data" "${TMP_DIR}/worker3/input" "${WORKER3_LOG}"
wait_for_http "http://${WORKER2_ADDR}/healthz" "worker2" "${WORKER2_LOG}"
wait_for_http "http://${WORKER3_ADDR}/healthz" "worker3" "${WORKER3_LOG}"

start_worker 1 ":19111" "${WORKER1_ADDR}" "${TMP_DIR}/worker1/data" "${TMP_DIR}/worker1/input" "${WORKER1_LOG}"
wait_for_http "http://${WORKER1_ADDR}/healthz" "worker1" "${WORKER1_LOG}"

for i in $(seq 0 9); do
  video_name="video_${i}.mp4"
  response="$(wait_for_result "${video_name}")"
  result_uri="$(printf '%s' "${response}" | json_get result.uri)"
  result_path="${TMP_DIR}/result_${i}.gif"
  curl -fsS "${result_uri}" -o "${result_path}"
  if ! cmp -s "${TMP_DIR}/worker1/input/${video_name}" "${result_path}"; then
    echo "result mismatch for ${video_name}" >&2
    echo "response: ${response}" >&2
    exit 1
  fi
  echo "verified ${video_name}"
done

echo "worker cluster smoke passed"
