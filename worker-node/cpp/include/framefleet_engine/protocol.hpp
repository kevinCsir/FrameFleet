#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace framefleet_engine {

constexpr int kProtocolVersion = 1;

struct FileRef {
    std::string mode;
    std::string path;
    std::string name;
    std::int64_t size_bytes = 0;
};

struct Request {
    int version = 0;
    std::string request_id;
    std::string op;
    std::string job_id;
    std::string task_id;
    int segment_index = 0;
    int segment_count = 0;
    std::optional<FileRef> input;
    std::vector<FileRef> inputs;
    std::optional<FileRef> output;
    std::string output_dir;
};

struct SegmentFile {
    int segment_index = 0;
    std::string path;
    std::string name;
    std::int64_t size_bytes = 0;
};

struct Response {
    int version = kProtocolVersion;
    std::string request_id;
    std::string type;
    std::string job_id;
    std::string task_id;
    int segment_index = 0;
    std::string result_name;
    std::string artifact_name;
    std::string checksum;
    int frame_count = 0;
    std::int64_t duration_ms = 0;
    std::int64_t output_size_bytes = 0;
    std::vector<SegmentFile> segments;
    std::string reason;
    bool retryable = false;
};

Request parse_request_line(const std::string& line);
nlohmann::json response_to_json(const Response& response);

Response make_completed_response(const Request& request);
Response make_failed_response(const std::string& request_id,
                              const std::string& job_id,
                              const std::string& task_id,
                              const std::string& reason,
                              bool retryable);
Response make_failed_response(const Request& request, const std::string& reason, bool retryable);

std::string basename_from_path(const std::string& path);

}  // namespace framefleet_engine
