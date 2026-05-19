#!/usr/bin/env bash
set -euo pipefail

ENTRY_ADDR="${ENTRY_ADDR:-127.0.0.1:18081}"
ENTRY_URL="${ENTRY_URL:-http://${ENTRY_ADDR}}"
WORKER_ADDRESS="${WORKER_ADDRESS:-127.0.0.1:19101}"
SECOND_WORKER_ADDRESS="${SECOND_WORKER_ADDRESS:-127.0.0.1:19102}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENTRY_LOG="$(mktemp)"

ENTRY_PID=""
cleanup() {
  if [[ -n "${ENTRY_PID}" ]] && kill -0 "${ENTRY_PID}" 2>/dev/null; then
    kill "${ENTRY_PID}" 2>/dev/null || true
    wait "${ENTRY_PID}" 2>/dev/null || true
  fi
  rm -f "${ENTRY_LOG}"
}
trap cleanup EXIT

json_get() {
  local expr="$1"
  python3 -c 'import json,sys
data=json.load(sys.stdin)
value=data
for part in sys.argv[1].split("."):
    if part == "":
        continue
    if part.isdigit():
        value=value[int(part)]
    else:
        value=value[part]
print(value)' "${expr}"
}

assert_status() {
  local response="$1"
  local expected="$2"
  local actual
  actual="$(printf '%s' "${response}" | json_get status)"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "expected status ${expected}, got ${actual}" >&2
    echo "response: ${response}" >&2
    exit 1
  fi
}

post_json() {
  local path="$1"
  local payload="$2"
  curl -sS -X POST "${ENTRY_URL}${path}" \
    -H 'Content-Type: application/json' \
    -d "${payload}"
}

get_json() {
  local path="$1"
  curl -sS "${ENTRY_URL}${path}"
}

wait_for_entry() {
  for _ in $(seq 1 50); do
    if curl -sS -X POST "${ENTRY_URL}/workers/register" \
      -H 'Content-Type: application/json' \
      -d "{\"address\":\"${WORKER_ADDRESS}\",\"total_slots\":1,\"supported_tasks\":[\"process_segment\",\"assemble_gif\"],\"disk_total_bytes\":1000000000,\"disk_free_bytes\":800000000}" >/tmp/framefleet_register_probe.json 2>/dev/null; then
      cat /tmp/framefleet_register_probe.json
      rm -f /tmp/framefleet_register_probe.json
      return 0
    fi
    sleep 0.1
  done

  echo "entry server did not become ready" >&2
  echo "entry log:" >&2
  cat "${ENTRY_LOG}" >&2 || true
  return 1
}

cd "${ROOT_DIR}"

echo "starting entry server at ${ENTRY_ADDR}"
source ~/.zshrc
ENTRY_SERVER_ADDR="${ENTRY_ADDR}" \
LOG_OUTPUT=discard \
GOCACHE=/tmp/go-build \
GOMODCACHE=/tmp/go/pkg/mod \
go run ./entry-server/cmd/server >"${ENTRY_LOG}" 2>&1 &
ENTRY_PID="$!"

register_response="$(wait_for_entry)"
assert_status "${register_response}" "success"
worker_id="$(printf '%s' "${register_response}" | json_get worker_id)"
echo "registered worker ${worker_id}"

heartbeat_response="$(post_json "/workers/heartbeat" "{\"worker_id\":\"${worker_id}\",\"total_slots\":1,\"running_process_segment\":0,\"running_assemble_gif\":0,\"running_tasks\":[],\"disk_total_bytes\":1000000000,\"disk_free_bytes\":800000000}")"
assert_status "${heartbeat_response}" "success"
echo "heartbeat ok"

job_response="$(post_json "/jobs" "{\"worker_id\":\"${worker_id}\",\"video_name\":\"demo.mp4\",\"segment_count\":1,\"total_size_bytes\":123,\"mode\":\"external\"}")"
assert_status "${job_response}" "success"
job_id="$(printf '%s' "${job_response}" | json_get job_id)"
task_id="$(printf '%s' "${job_response}" | json_get assignments.0.task_id)"
echo "created job ${job_id}, task ${task_id}"

duplicate_response="$(post_json "/jobs" "{\"worker_id\":\"${worker_id}\",\"video_name\":\"demo.mp4\",\"segment_count\":1,\"total_size_bytes\":123,\"mode\":\"external\"}")"
assert_status "${duplicate_response}" "already_exists"
echo "duplicate job returned already_exists"

accepted_response="$(post_json "/tasks/${task_id}/accepted" "{\"worker_id\":\"${worker_id}\"}")"
assert_status "${accepted_response}" "success"
echo "accepted ok"

completed_response="$(post_json "/tasks/${task_id}/completed" "{\"worker_id\":\"${worker_id}\",\"checksum\":\"sha256:test\",\"frame_count\":10,\"duration_ms\":1500,\"output_size_bytes\":2048}")"
assert_status "${completed_response}" "success"
echo "completed ok"

repeat_completed_response="$(post_json "/tasks/${task_id}/completed" "{\"worker_id\":\"${worker_id}\"}")"
assert_status "${repeat_completed_response}" "invalid_state"
echo "repeat completed returned invalid_state"

second_job_response="$(post_json "/jobs" "{\"worker_id\":\"${worker_id}\",\"video_name\":\"second.mp4\",\"segment_count\":1,\"total_size_bytes\":456,\"mode\":\"external\"}")"
assert_status "${second_job_response}" "success"
second_task_id="$(printf '%s' "${second_job_response}" | json_get assignments.0.task_id)"
echo "slot released after completed, second task ${second_task_id}"

busy_job_response="$(post_json "/jobs" "{\"worker_id\":\"${worker_id}\",\"video_name\":\"busy.mp4\",\"segment_count\":1,\"total_size_bytes\":789,\"mode\":\"external\"}")"
assert_status "${busy_job_response}" "insufficient_resources"
echo "reserved slot blocks extra job"

failed_response="$(post_json "/tasks/${second_task_id}/failed" "{\"worker_id\":\"${worker_id}\",\"reason\":\"smoke failure\",\"retryable\":true}")"
assert_status "${failed_response}" "success"
echo "failed ok"

third_job_response="$(post_json "/jobs" "{\"worker_id\":\"${worker_id}\",\"video_name\":\"third.mp4\",\"segment_count\":1,\"total_size_bytes\":999,\"mode\":\"external\"}")"
assert_status "${third_job_response}" "success"
echo "slot released after failed"

internal_job_response="$(post_json "/jobs" "{\"worker_id\":\"${worker_id}\",\"video_name\":\"internal.mp4\",\"segment_count\":1,\"total_size_bytes\":321,\"mode\":\"internal\"}")"
assert_status "${internal_job_response}" "insufficient_resources"
echo "internal job respects source worker slot availability"

second_register_response="$(post_json "/workers/register" "{\"address\":\"${SECOND_WORKER_ADDRESS}\",\"total_slots\":2,\"supported_tasks\":[\"process_segment\",\"assemble_gif\"],\"disk_total_bytes\":1000000000,\"disk_free_bytes\":800000000}")"
assert_status "${second_register_response}" "success"
second_worker_id="$(printf '%s' "${second_register_response}" | json_get worker_id)"
second_heartbeat_response="$(post_json "/workers/heartbeat" "{\"worker_id\":\"${second_worker_id}\",\"total_slots\":2,\"running_process_segment\":0,\"running_assemble_gif\":0,\"running_tasks\":[],\"disk_total_bytes\":1000000000,\"disk_free_bytes\":800000000}")"
assert_status "${second_heartbeat_response}" "success"

internal_success_response="$(post_json "/jobs" "{\"worker_id\":\"${second_worker_id}\",\"video_name\":\"internal-ok.mp4\",\"segment_count\":2,\"total_size_bytes\":654,\"mode\":\"internal\"}")"
assert_status "${internal_success_response}" "success"
internal_job_id="$(printf '%s' "${internal_success_response}" | json_get job_id)"
internal_task_id_0="$(printf '%s' "${internal_success_response}" | json_get assignments.0.task_id)"
internal_task_id_1="$(printf '%s' "${internal_success_response}" | json_get assignments.1.task_id)"
internal_first_worker="$(printf '%s' "${internal_success_response}" | json_get assignments.0.worker_id)"
internal_second_worker="$(printf '%s' "${internal_success_response}" | json_get assignments.1.worker_id)"
if [[ "${internal_first_worker}" != "${second_worker_id}" || "${internal_second_worker}" != "${second_worker_id}" ]]; then
  echo "internal job assignments should stay on source worker" >&2
  echo "response: ${internal_success_response}" >&2
  exit 1
fi
echo "internal job assigns source worker"

internal_duplicate_response="$(post_json "/jobs" "{\"worker_id\":\"${second_worker_id}\",\"video_name\":\"internal-ok.mp4\",\"segment_count\":2,\"total_size_bytes\":654,\"mode\":\"internal\"}")"
assert_status "${internal_duplicate_response}" "already_exists"
echo "internal duplicate returned already_exists"

internal_completed_0="$(post_json "/tasks/${internal_task_id_0}/completed" "{\"worker_id\":\"${second_worker_id}\",\"frame_count\":5,\"duration_ms\":500,\"output_size_bytes\":1024}")"
assert_status "${internal_completed_0}" "success"
internal_completed_1="$(post_json "/tasks/${internal_task_id_1}/completed" "{\"worker_id\":\"${second_worker_id}\",\"frame_count\":5,\"duration_ms\":600,\"output_size_bytes\":2048}")"
assert_status "${internal_completed_1}" "success"
echo "internal segment tasks completed"

assembled_response="$(post_json "/jobs/${internal_job_id}/assembled" "{\"worker_id\":\"${second_worker_id}\",\"status\":\"success\",\"result_name\":\"internal-ok.gif\",\"checksum\":\"sha256:final\",\"duration_ms\":1200,\"output_size_bytes\":4096}")"
assert_status "${assembled_response}" "success"
echo "internal assembled success"

query_response="$(get_json "/jobs/result?address=${SECOND_WORKER_ADDRESS}&video_name=internal-ok.mp4")"
assert_status "${query_response}" "success"
query_job_status="$(printf '%s' "${query_response}" | json_get job_status)"
query_result_name="$(printf '%s' "${query_response}" | json_get result.name)"
if [[ "${query_job_status}" != "completed" || "${query_result_name}" != "internal-ok.gif" ]]; then
  echo "unexpected query result" >&2
  echo "response: ${query_response}" >&2
  exit 1
fi
echo "result query returns completed gif location"

missing_query_response="$(get_json "/jobs/result?address=${SECOND_WORKER_ADDRESS}&video_name=missing.mp4")"
assert_status "${missing_query_response}" "not_found"
echo "missing result query returned not_found"

repeat_assembled_response="$(post_json "/jobs/${internal_job_id}/assembled" "{\"worker_id\":\"${second_worker_id}\",\"status\":\"success\"}")"
assert_status "${repeat_assembled_response}" "invalid_state"
echo "repeat assembled returned invalid_state"

echo "smoke task lifecycle passed"
