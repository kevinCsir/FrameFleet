#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENTRY_ADDR="${ENTRY_ADDR:-127.0.0.1:18083}"
ENTRY_URL="http://${ENTRY_ADDR}"
WORKER_ADDR="${WORKER_ADDR:-127.0.0.1:19121}"
ENGINE_BINARY="${ENGINE_BINARY:-${ROOT_DIR}/worker-node/cpp/build/framefleet-engine}"
TEST_VIDEO="${TEST_VIDEO:-${ROOT_DIR}/worker-node/cpp/testdata/videos/canny_source_short.mp4}"
VIDEO_NAME="${VIDEO_NAME:-canny_source_short.mp4}"
CURL_TIMEOUT_SECONDS="${CURL_TIMEOUT_SECONDS:-5}"
RESULT_WAIT_SECONDS="${RESULT_WAIT_SECONDS:-120}"
SPLIT_MAX_SEGMENTS="${SPLIT_MAX_SEGMENTS:-2}"
PROCESS_CANNY_LOW_THRESHOLD="${PROCESS_CANNY_LOW_THRESHOLD:-180}"
PROCESS_CANNY_HIGH_THRESHOLD="${PROCESS_CANNY_HIGH_THRESHOLD:-360}"

TMP_DIR="$(mktemp -d)"
KEEP_LOGS="${KEEP_LOGS:-0}"
ENTRY_LOG="${TMP_DIR}/entry.log"
WORKER_LOG="${TMP_DIR}/worker.log"
RESULT_PATH="${TMP_DIR}/result.gif"
PIDS=()
SMOKE_STATUS=1

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
  cat "${log_path}" >&2 || true
  return 1
}

dump_logs() {
  echo "entry log:" >&2
  tail -n 200 "${ENTRY_LOG}" >&2 || true
  echo "worker log:" >&2
  tail -n 200 "${WORKER_LOG}" >&2 || true
}

query_result() {
  curl_local -fsS "${ENTRY_URL}/jobs/result?address=${WORKER_ADDR}&video_name=${VIDEO_NAME}"
}

assert_gif() {
  local path="$1"
  local header
  header="$(head -c 6 "${path}")"
  if [[ "${header}" != "GIF87a" && "${header}" != "GIF89a" ]]; then
    echo "result is not a GIF: ${path}" >&2
    file "${path}" >&2 || true
    return 1
  fi
}

cd "${ROOT_DIR}"
source ~/.zshrc

if [[ ! -x "${ENGINE_BINARY}" ]]; then
  echo "engine binary not found at ${ENGINE_BINARY}" >&2
  echo "build it with: cmake -S worker-node/cpp -B worker-node/cpp/build && cmake --build worker-node/cpp/build" >&2
  exit 1
fi
if [[ ! -f "${TEST_VIDEO}" ]]; then
  echo "test video not found at ${TEST_VIDEO}" >&2
  exit 1
fi

mkdir -p "${TMP_DIR}/worker/input"
cp "${TEST_VIDEO}" "${TMP_DIR}/worker/input/${VIDEO_NAME}"

ENTRY_SERVER_ADDR="${ENTRY_ADDR}" \
LOG_OUTPUT=file \
LOG_FILE="${ENTRY_LOG}" \
SPLIT_MAX_SEGMENTS="${SPLIT_MAX_SEGMENTS}" \
PROCESS_CANNY_LOW_THRESHOLD="${PROCESS_CANNY_LOW_THRESHOLD}" \
PROCESS_CANNY_HIGH_THRESHOLD="${PROCESS_CANNY_HIGH_THRESHOLD}" \
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./entry-server/cmd/server >/dev/null 2>&1 &
PIDS+=("$!")

wait_for_http "${ENTRY_URL}/jobs/result?address=probe&video_name=probe" "entry" "${ENTRY_LOG}"

WORKER_LISTEN_ADDR=":${WORKER_ADDR##*:}" \
WORKER_ADVERTISED_ADDRESS="${WORKER_ADDR}" \
ENTRY_BASE_URL="${ENTRY_URL}" \
WORKER_TOTAL_SLOTS=1 \
WORKER_DATA_DIR="${TMP_DIR}/worker/data" \
WORKER_INPUT_DIR="${TMP_DIR}/worker/input" \
WORKER_SOURCE_SCAN_INTERVAL_SECONDS=1 \
WORKER_HEARTBEAT_INTERVAL_SECONDS=1 \
WORKER_ENGINE_BINARY="${ENGINE_BINARY}" \
WORKER_LOG_OUTPUT=file \
WORKER_LOG_FILE="${WORKER_LOG}" \
GIN_MODE=release \
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./worker-node/go/cmd/worker-agent >/dev/null 2>&1 &
PIDS+=("$!")

wait_for_http "http://${WORKER_ADDR}/healthz" "worker" "${WORKER_LOG}"

deadline=$((SECONDS + RESULT_WAIT_SECONDS))
while [[ "${SECONDS}" -lt "${deadline}" ]]; do
  response=""
  if response="$(query_result 2>/dev/null)"; then
    status="$(printf '%s' "${response}" | json_get status || true)"
    job_status="$(printf '%s' "${response}" | json_get job_status || true)"
    if [[ "${status}" == "success" && "${job_status}" == "completed" ]]; then
      result_uri="$(printf '%s' "${response}" | json_get result.uri)"
      curl_local -fsS "${result_uri}" -o "${RESULT_PATH}"
      assert_gif "${RESULT_PATH}" || {
        dump_logs
        exit 1
      }
      echo "single real worker smoke passed"
      echo "result: ${RESULT_PATH}"
      SMOKE_STATUS=0
      exit 0
    fi
    if [[ "${job_status}" == "failed" ]]; then
      echo "job failed: ${response}" >&2
      dump_logs
      exit 1
    fi
  fi
  sleep 0.5
done

echo "timed out waiting for result after ${RESULT_WAIT_SECONDS}s" >&2
dump_logs
exit 1
