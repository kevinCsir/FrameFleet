#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENTRY_ADDR="${ENTRY_ADDR:-127.0.0.1:18084}"
ENTRY_URL="http://${ENTRY_ADDR}"
WORKER1_ADDR="${WORKER1_ADDR:-127.0.0.1:19131}"
WORKER2_ADDR="${WORKER2_ADDR:-127.0.0.1:19132}"
WORKER3_ADDR="${WORKER3_ADDR:-127.0.0.1:19133}"
WORKER4_ADDR="${WORKER4_ADDR:-127.0.0.1:19134}"
ENGINE_BINARY="${ENGINE_BINARY:-${ROOT_DIR}/worker-node/cpp/build/framefleet-engine}"
TEST_VIDEO="${TEST_VIDEO:-${ROOT_DIR}/worker-node/cpp/testdata/videos/canny_source_short.mp4}"
CURL_TIMEOUT_SECONDS="${CURL_TIMEOUT_SECONDS:-5}"
RESULT_WAIT_SECONDS="${RESULT_WAIT_SECONDS:-600}"
SPLIT_TARGET_SEGMENT_DURATION_MS="${SPLIT_TARGET_SEGMENT_DURATION_MS:-3000}"
SPLIT_MAX_SEGMENTS="${SPLIT_MAX_SEGMENTS:-8}"
WORKER_CANNY_LOW_THRESHOLD="${WORKER_CANNY_LOW_THRESHOLD:-180}"
WORKER_CANNY_HIGH_THRESHOLD="${WORKER_CANNY_HIGH_THRESHOLD:-360}"

TMP_DIR="$(mktemp -d)"
KEEP_LOGS="${KEEP_LOGS:-0}"
ENTRY_LOG="${TMP_DIR}/entry.log"
WORKER_LOGS=("${TMP_DIR}/worker1.log" "${TMP_DIR}/worker2.log" "${TMP_DIR}/worker3.log" "${TMP_DIR}/worker4.log")
WORKER_ADDRS=("${WORKER1_ADDR}" "${WORKER2_ADDR}" "${WORKER3_ADDR}" "${WORKER4_ADDR}")
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
  for worker_index in 1 2 3 4; do
    echo "worker${worker_index} log:" >&2
    tail -n 200 "${WORKER_LOGS[$((worker_index - 1))]}" >&2 || true
  done
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

generate_looped_video() {
  local source="$1"
  local repeats="$2"
  local output="$3"
  local list_file="${output}.concat.txt"
  : >"${list_file}"
  for _ in $(seq 1 "${repeats}"); do
    printf "file '%s'\n" "${source}" >>"${list_file}"
  done
  ffmpeg -hide_banner -v error -nostdin -y \
    -f concat -safe 0 -i "${list_file}" \
    -c copy "${output}"
}

start_entry() {
  ENTRY_SERVER_ADDR="${ENTRY_ADDR}" \
  LOG_OUTPUT=file \
  LOG_FILE="${ENTRY_LOG}" \
  SPLIT_TARGET_SEGMENT_DURATION_MS="${SPLIT_TARGET_SEGMENT_DURATION_MS}" \
  SPLIT_MAX_SEGMENTS="${SPLIT_MAX_SEGMENTS}" \
  GOCACHE=/tmp/go-build \
  GOMODCACHE=/tmp/go/pkg/mod \
  go run ./entry-server/cmd/server >/dev/null 2>&1 &
  PIDS+=("$!")
}

start_worker() {
  local worker_index="$1"
  local listen_port="$2"
  local advertised_addr="$3"
  local data_dir="$4"
  local input_dir="$5"
  local log_path="$6"

  WORKER_LISTEN_ADDR=":${listen_port}" \
  WORKER_ADVERTISED_ADDRESS="${advertised_addr}" \
  ENTRY_BASE_URL="${ENTRY_URL}" \
  WORKER_TOTAL_SLOTS=1 \
  WORKER_DATA_DIR="${data_dir}" \
  WORKER_INPUT_DIR="${input_dir}" \
  WORKER_SOURCE_SCAN_INTERVAL_SECONDS=1 \
  WORKER_HEARTBEAT_INTERVAL_SECONDS=1 \
  WORKER_ENGINE_BINARY="${ENGINE_BINARY}" \
  WORKER_CANNY_LOW_THRESHOLD="${WORKER_CANNY_LOW_THRESHOLD}" \
  WORKER_CANNY_HIGH_THRESHOLD="${WORKER_CANNY_HIGH_THRESHOLD}" \
  WORKER_LOG_OUTPUT=file \
  WORKER_LOG_FILE="${log_path}" \
  GIN_MODE=release \
  GOCACHE=/tmp/go-build \
  GOMODCACHE=/tmp/go/pkg/mod \
  go run ./worker-node/go/cmd/worker-agent >/dev/null 2>&1 &
  PIDS+=("$!")
  echo "started worker${worker_index} at ${advertised_addr}"
}

query_result() {
  local source_addr="$1"
  local video_name="$2"
  curl_local -fsS "${ENTRY_URL}/jobs/result?address=${source_addr}&video_name=${video_name}"
}

verify_all_results() {
  local pending=(
    "1:video_1x.mp4"
    "1:video_2x.mp4"
    "1:video_4x.mp4"
    "2:video_1x.mp4"
    "2:video_2x.mp4"
    "2:video_4x.mp4"
  )
  local completed=0
  local deadline=$((SECONDS + RESULT_WAIT_SECONDS))

  while [[ "${#pending[@]}" -gt 0 && "${SECONDS}" -lt "${deadline}" ]]; do
    local next_pending=()
    local made_progress=0

    for item in "${pending[@]}"; do
      local source_index="${item%%:*}"
      local video_name="${item##*:}"
      local source_addr="${WORKER_ADDRS[$((source_index - 1))]}"
      local response=""

      if response="$(query_result "${source_addr}" "${video_name}" 2>/dev/null)"; then
        local status
        local job_status
        status="$(printf '%s' "${response}" | json_get status || true)"
        job_status="$(printf '%s' "${response}" | json_get job_status || true)"
        if [[ "${status}" == "success" && "${job_status}" == "completed" ]]; then
          local result_uri
          local result_path
          result_uri="$(printf '%s' "${response}" | json_get result.uri)"
          result_path="${TMP_DIR}/result_worker${source_index}_${video_name%.mp4}.gif"
          curl_local -fsS "${result_uri}" -o "${result_path}"
          assert_gif "${result_path}" || {
            dump_logs
            return 1
          }
          completed=$((completed + 1))
          made_progress=1
          echo "verified worker${source_index}/${video_name}"
          continue
        fi
        if [[ "${job_status}" == "failed" ]]; then
          echo "job failed for worker${source_index}/${video_name}: ${response}" >&2
          dump_logs
          return 1
        fi
      fi

      next_pending+=("${item}")
    done

    pending=("${next_pending[@]}")
    if [[ "${#pending[@]}" -gt 0 ]]; then
      if [[ "${made_progress}" -eq 1 ]]; then
        echo "verified ${completed}/6, pending ${#pending[@]}"
      fi
      sleep 0.5
    fi
  done

  if [[ "${#pending[@]}" -gt 0 ]]; then
    echo "timed out waiting for ${#pending[@]} result(s) after ${RESULT_WAIT_SECONDS}s" >&2
    printf 'pending: %s\n' "${pending[@]}" >&2
    dump_logs
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

for worker_index in 1 2 3 4; do
  mkdir -p "${TMP_DIR}/worker${worker_index}/input"
done

for source_index in 1 2; do
  cp "${TEST_VIDEO}" "${TMP_DIR}/worker${source_index}/input/video_1x.mp4"
  generate_looped_video "${TEST_VIDEO}" 2 "${TMP_DIR}/worker${source_index}/input/video_2x.mp4"
  generate_looped_video "${TEST_VIDEO}" 4 "${TMP_DIR}/worker${source_index}/input/video_4x.mp4"
done

start_entry
wait_for_http "${ENTRY_URL}/jobs/result?address=probe&video_name=probe" "entry" "${ENTRY_LOG}"

for worker_index in 1 2 3 4; do
  port=$((19130 + worker_index))
  start_worker "${worker_index}" "${port}" "${WORKER_ADDRS[$((worker_index - 1))]}" \
    "${TMP_DIR}/worker${worker_index}/data" "${TMP_DIR}/worker${worker_index}/input" \
    "${WORKER_LOGS[$((worker_index - 1))]}"
done
for worker_index in 1 2 3 4; do
  wait_for_http "http://${WORKER_ADDRS[$((worker_index - 1))]}/healthz" \
    "worker${worker_index}" "${WORKER_LOGS[$((worker_index - 1))]}"
done

verify_all_results

segment_completed_count="$(grep -c 'event":"segment_task_completed' "${ENTRY_LOG}" || true)"
assembled_count="$(grep -c 'event":"job_assembled' "${ENTRY_LOG}" || true)"
echo "segment_task_completed=${segment_completed_count}"
echo "job_assembled=${assembled_count}"

for worker_index in 1 2 3 4; do
  result_count="$(find "${TMP_DIR}/worker${worker_index}/data/spool/results" -type f -name '*.gif' 2>/dev/null | wc -l)"
  echo "worker${worker_index}_result_files=${result_count}"
done

SMOKE_STATUS=0
echo "cluster real worker smoke passed"
