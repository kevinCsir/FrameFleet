#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENTRY_ADDR="${ENTRY_ADDR:-127.0.0.1:18082}"
ENTRY_URL="http://${ENTRY_ADDR}"
WORKER1_ADDR="${WORKER1_ADDR:-127.0.0.1:19111}"
WORKER2_ADDR="${WORKER2_ADDR:-127.0.0.1:19112}"
WORKER3_ADDR="${WORKER3_ADDR:-127.0.0.1:19113}"
WORKER4_ADDR="${WORKER4_ADDR:-127.0.0.1:19114}"
ENGINE_BINARY="${ENGINE_BINARY:-${ROOT_DIR}/worker-node/cpp/build/framefleet-engine}"
WORKER_COUNT="${WORKER_COUNT:-4}"
SOURCE_WORKER_COUNT="${SOURCE_WORKER_COUNT:-2}"
VIDEO_COUNT_PER_SOURCE="${VIDEO_COUNT_PER_SOURCE:-30}"
SPLIT_MAX_SEGMENTS="${SPLIT_MAX_SEGMENTS:-7}"
CURL_TIMEOUT_SECONDS="${CURL_TIMEOUT_SECONDS:-5}"
RESULT_WAIT_SECONDS="${RESULT_WAIT_SECONDS:-300}"

TMP_DIR="$(mktemp -d)"
KEEP_LOGS="${KEEP_LOGS:-0}"
ENTRY_LOG="${TMP_DIR}/entry.log"
WORKER1_LOG="${TMP_DIR}/worker1.log"
WORKER2_LOG="${TMP_DIR}/worker2.log"
WORKER3_LOG="${TMP_DIR}/worker3.log"
WORKER4_LOG="${TMP_DIR}/worker4.log"
PIDS=()
SMOKE_STATUS=1

WORKER_ADDRS=("${WORKER1_ADDR}" "${WORKER2_ADDR}" "${WORKER3_ADDR}" "${WORKER4_ADDR}")
WORKER_LOGS=("${WORKER1_LOG}" "${WORKER2_LOG}" "${WORKER3_LOG}" "${WORKER4_LOG}")

cleanup() {
  for pid in "${PIDS[@]}"; do
    if kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" 2>/dev/null || true
    fi
  done
  for pid in "${PIDS[@]}"; do
    wait "${pid}" 2>/dev/null || true
  done
  if [[ "${SMOKE_STATUS}" -eq 0 && "${KEEP_LOGS}" != "1" ]]; then
    rm -rf "${TMP_DIR}"
  else
    echo "smoke logs kept at ${TMP_DIR}" >&2
  fi
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

curl_local() {
  curl --noproxy '*' --max-time "${CURL_TIMEOUT_SECONDS}" "$@"
}

wait_for_http() {
  local url="$1"
  local name="$2"
  local log_path="$3"
  for _ in $(seq 1 120); do
    if curl_local -sS "${url}" >/dev/null 2>&1; then
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
  local source_addr="$1"
  local video_name="$2"
  curl_local -fsS "${ENTRY_URL}/jobs/result?address=${source_addr}&video_name=${video_name}"
}

dump_logs() {
  echo "entry log:" >&2
  tail -n 200 "${ENTRY_LOG}" >&2 || true
  echo "worker1 log:" >&2
  tail -n 200 "${WORKER1_LOG}" >&2 || true
  echo "worker2 log:" >&2
  tail -n 200 "${WORKER2_LOG}" >&2 || true
  echo "worker3 log:" >&2
  tail -n 200 "${WORKER3_LOG}" >&2 || true
  echo "worker4 log:" >&2
  tail -n 200 "${WORKER4_LOG}" >&2 || true
}

verify_result() {
  local source_index="$1"
  local video_index="$2"
  local video_name="$3"
  local response="$4"
  local result_uri
  local result_path

  result_uri="$(printf '%s' "${response}" | json_get result.uri)"
  result_path="${TMP_DIR}/result_worker${source_index}_${video_index}.gif"
  curl_local -fsS "${result_uri}" -o "${result_path}"
  if ! cmp -s "${TMP_DIR}/worker${source_index}/input/${video_name}" "${result_path}"; then
    echo "result mismatch for ${video_name}" >&2
    echo "response: ${response}" >&2
    return 1
  fi
  echo "verified worker${source_index}/${video_name}"
}

verify_all_results() {
  local pending=()
  local deadline=$((SECONDS + RESULT_WAIT_SECONDS))
  local completed=0
  local total=$((SOURCE_WORKER_COUNT * VIDEO_COUNT_PER_SOURCE))

  for source_index in $(seq 1 "${SOURCE_WORKER_COUNT}"); do
    for video_index in $(seq 0 $((VIDEO_COUNT_PER_SOURCE - 1))); do
      pending+=("${source_index}:${video_index}")
    done
  done

  while [[ "${#pending[@]}" -gt 0 && "${SECONDS}" -lt "${deadline}" ]]; do
    local next_pending=()
    local made_progress=0

    for item in "${pending[@]}"; do
      local source_index="${item%%:*}"
      local video_index="${item##*:}"
      local source_addr="${WORKER_ADDRS[$((source_index - 1))]}"
      local video_name="video_${video_index}.mp4"
      local response=""
      if response="$(query_result "${source_addr}" "${video_name}" 2>/dev/null)"; then
        local status
        local job_status
        status="$(printf '%s' "${response}" | json_get status || true)"
        job_status="$(printf '%s' "${response}" | json_get job_status || true)"

        if [[ "${status}" == "success" && "${job_status}" == "completed" ]]; then
          verify_result "${source_index}" "${video_index}" "${video_name}" "${response}" || {
            dump_logs
            return 1
          }
          completed=$((completed + 1))
          made_progress=1
          continue
        fi

        if [[ "${job_status}" == "failed" ]]; then
          echo "job worker${source_index}/${video_name} failed: ${response}" >&2
          dump_logs
          return 1
        fi
      fi
      next_pending+=("${item}")
    done

    pending=("${next_pending[@]}")
    if [[ "${#pending[@]}" -gt 0 ]]; then
      if [[ "${made_progress}" -eq 1 ]]; then
        echo "verified ${completed}/${total}, pending ${#pending[@]}"
      fi
      sleep 0.5
    fi
  done

  if [[ "${#pending[@]}" -gt 0 ]]; then
    echo "timed out waiting for ${#pending[@]} result(s) after ${RESULT_WAIT_SECONDS}s" >&2
    echo "pending videos:" >&2
    for item in "${pending[@]}"; do
      local source_index="${item%%:*}"
      local video_index="${item##*:}"
      echo "worker${source_index}/video_${video_index}.mp4" >&2
    done
    dump_logs
    return 1
  fi
}

start_entry() {
  ENTRY_SERVER_ADDR="${ENTRY_ADDR}" \
  LOG_OUTPUT=file \
  LOG_FILE="${ENTRY_LOG}" \
  SPLIT_MAX_SEGMENTS="${SPLIT_MAX_SEGMENTS}" \
  GOCACHE=/tmp/go-build \
  GOMODCACHE=/tmp/go/pkg/mod \
  go run ./entry-server/cmd/server >/dev/null 2>&1 &
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
  WORKER_LOG_OUTPUT=file \
  WORKER_LOG_FILE="${log_path}" \
  GIN_MODE=release \
  GOCACHE=/tmp/go-build \
  GOMODCACHE=/tmp/go/pkg/mod \
  go run ./worker-node/go/cmd/worker-agent >/dev/null 2>&1 &
  PIDS+=("$!")
  echo "started worker ${index} at ${advertised_addr}"
}

cd "${ROOT_DIR}"

if [[ "${WORKER_COUNT}" -lt 1 || "${WORKER_COUNT}" -gt 4 ]]; then
  echo "WORKER_COUNT must be between 1 and 4" >&2
  exit 1
fi
if [[ "${SOURCE_WORKER_COUNT}" -lt 1 || "${SOURCE_WORKER_COUNT}" -gt "${WORKER_COUNT}" ]]; then
  echo "SOURCE_WORKER_COUNT must be between 1 and WORKER_COUNT" >&2
  exit 1
fi

if [[ ! -x "${ENGINE_BINARY}" ]]; then
  echo "engine binary not found at ${ENGINE_BINARY}" >&2
  echo "build it with: cmake -S worker-node/cpp -B worker-node/cpp/build && cmake --build worker-node/cpp/build" >&2
  exit 1
fi

for worker_index in $(seq 1 "${WORKER_COUNT}"); do
  mkdir -p "${TMP_DIR}/worker${worker_index}/input"
done
for source_index in $(seq 1 "${SOURCE_WORKER_COUNT}"); do
  for video_index in $(seq 0 $((VIDEO_COUNT_PER_SOURCE - 1))); do
    printf 'framefleet-worker-%02d-video-%03d:abcdefghijklmnopqrstuvwxyz:%02d:%03d\n' \
      "${source_index}" "${video_index}" "${source_index}" "${video_index}" \
      >"${TMP_DIR}/worker${source_index}/input/video_${video_index}.mp4"
  done
done

start_entry
wait_for_http "${ENTRY_URL}/jobs/result?address=probe&video_name=probe" "entry" "${ENTRY_LOG}"

echo "smoke logs: ${TMP_DIR}"
echo "entry log: ${ENTRY_LOG}"
echo "worker1 log: ${WORKER1_LOG}"
echo "worker2 log: ${WORKER2_LOG}"
echo "worker3 log: ${WORKER3_LOG}"
echo "worker4 log: ${WORKER4_LOG}"

for worker_index in $(seq 1 "${WORKER_COUNT}"); do
  port=$((19110 + worker_index))
  start_worker "${worker_index}" ":${port}" "${WORKER_ADDRS[$((worker_index - 1))]}" \
    "${TMP_DIR}/worker${worker_index}/data" "${TMP_DIR}/worker${worker_index}/input" \
    "${WORKER_LOGS[$((worker_index - 1))]}"
done
for worker_index in $(seq 1 "${WORKER_COUNT}"); do
  wait_for_http "http://${WORKER_ADDRS[$((worker_index - 1))]}/healthz" \
    "worker${worker_index}" "${WORKER_LOGS[$((worker_index - 1))]}"
done

verify_all_results

SMOKE_STATUS=0
echo "worker cluster smoke passed"
