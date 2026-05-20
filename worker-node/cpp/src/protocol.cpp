#include "framefleet_engine/protocol.hpp"

#include <filesystem>
#include <stdexcept>

namespace framefleet_engine {
namespace {

std::string required_string(const nlohmann::json& json, const char* key) {
    if (!json.contains(key) || !json.at(key).is_string()) {
        throw std::runtime_error(std::string("missing or invalid string field: ") + key);
    }
    auto value = json.at(key).get<std::string>();
    if (value.empty()) {
        throw std::runtime_error(std::string("empty string field: ") + key);
    }
    return value;
}

int optional_int(const nlohmann::json& json, const char* key, int fallback) {
    if (!json.contains(key)) {
        return fallback;
    }
    if (!json.at(key).is_number_integer()) {
        throw std::runtime_error(std::string("invalid integer field: ") + key);
    }
    return json.at(key).get<int>();
}

std::int64_t optional_int64(const nlohmann::json& json, const char* key, std::int64_t fallback) {
    if (!json.contains(key)) {
        return fallback;
    }
    if (!json.at(key).is_number_integer()) {
        throw std::runtime_error(std::string("invalid integer field: ") + key);
    }
    return json.at(key).get<std::int64_t>();
}

FileRef parse_file_ref(const nlohmann::json& json, const char* field_name) {
    if (!json.is_object()) {
        throw std::runtime_error(std::string("invalid file ref field: ") + field_name);
    }

    FileRef ref;
    ref.mode = required_string(json, "mode");
    ref.path = required_string(json, "path");
    if (json.contains("name")) {
        ref.name = required_string(json, "name");
    }
    ref.size_bytes = optional_int64(json, "size_bytes", 0);

    if (ref.mode != "file") {
        throw std::runtime_error("unsupported data mode: " + ref.mode);
    }

    return ref;
}

void validate_request(const Request& request) {
    if (request.version != kProtocolVersion) {
        throw std::runtime_error("unsupported protocol version");
    }
    if (request.request_id.empty()) {
        throw std::runtime_error("request_id is required");
    }

    if (request.op == "ping") {
        return;
    }
    if (request.op == "process_internal_simple") {
        if (!request.input || !request.output) {
            throw std::runtime_error("process_internal_simple requires input and output");
        }
        return;
    }
    if (request.op == "split_video") {
        if (!request.input || request.output_dir.empty() || request.segment_count <= 0) {
            throw std::runtime_error("split_video requires input, output_dir, and positive segment_count");
        }
        return;
    }
    if (request.op == "process_segment") {
        if (!request.input || !request.output) {
            throw std::runtime_error("process_segment requires input and output");
        }
        return;
    }
    if (request.op == "assemble_gif") {
        if (request.inputs.empty() || !request.output) {
            throw std::runtime_error("assemble_gif requires inputs and output");
        }
        return;
    }

    throw std::runtime_error("unknown op: " + request.op);
}

void set_if_not_empty(nlohmann::json& json, const char* key, const std::string& value) {
    if (!value.empty()) {
        json[key] = value;
    }
}

void set_if_positive(nlohmann::json& json, const char* key, std::int64_t value) {
    if (value > 0) {
        json[key] = value;
    }
}

}  // namespace

Request parse_request_line(const std::string& line) {
    auto json = nlohmann::json::parse(line);
    if (!json.is_object()) {
        throw std::runtime_error("request must be a JSON object");
    }

    Request request;
    if (!json.contains("version") || !json.at("version").is_number_integer()) {
        throw std::runtime_error("missing or invalid integer field: version");
    }
    request.version = json.at("version").get<int>();
    request.request_id = required_string(json, "request_id");
    request.op = required_string(json, "op");
    request.segment_count = optional_int(json, "segment_count", 0);
    if (json.contains("input")) {
        request.input = parse_file_ref(json.at("input"), "input");
    }
    if (json.contains("inputs")) {
        if (!json.at("inputs").is_array()) {
            throw std::runtime_error("inputs must be an array");
        }
        for (const auto& item : json.at("inputs")) {
            request.inputs.push_back(parse_file_ref(item, "inputs"));
        }
    }
    if (json.contains("output")) {
        request.output = parse_file_ref(json.at("output"), "output");
    }
    if (json.contains("output_dir")) {
        request.output_dir = required_string(json, "output_dir");
    }

    validate_request(request);
    return request;
}

nlohmann::json response_to_json(const Response& response) {
    nlohmann::json json{
        {"version", response.version},
        {"request_id", response.request_id},
        {"type", response.type},
    };

    set_if_not_empty(json, "result_name", response.result_name);
    set_if_not_empty(json, "artifact_name", response.artifact_name);
    set_if_not_empty(json, "checksum", response.checksum);
    if (response.frame_count > 0) {
        json["frame_count"] = response.frame_count;
    }
    set_if_positive(json, "duration_ms", response.duration_ms);
    set_if_positive(json, "output_size_bytes", response.output_size_bytes);

    if (!response.segments.empty()) {
        json["segments"] = nlohmann::json::array();
        for (const auto& segment : response.segments) {
            nlohmann::json segment_json{
                {"segment_index", segment.segment_index},
                {"path", segment.path},
            };
            set_if_not_empty(segment_json, "name", segment.name);
            set_if_positive(segment_json, "size_bytes", segment.size_bytes);
            json["segments"].push_back(segment_json);
        }
    }

    set_if_not_empty(json, "reason", response.reason);
    if (response.type == "failed") {
        json["retryable"] = response.retryable;
    }

    return json;
}

Response make_completed_response(const Request& request) {
    Response response;
    response.request_id = request.request_id;
    response.type = "completed";
    return response;
}

Response make_failed_response(const std::string& request_id,
                              const std::string& reason,
                              bool retryable) {
    Response response;
    response.request_id = request_id;
    response.type = "failed";
    response.reason = reason;
    response.retryable = retryable;
    return response;
}

Response make_failed_response(const Request& request, const std::string& reason, bool retryable) {
    return make_failed_response(request.request_id, reason, retryable);
}

std::string basename_from_path(const std::string& path) {
    return std::filesystem::path(path).filename().string();
}

}  // namespace framefleet_engine
