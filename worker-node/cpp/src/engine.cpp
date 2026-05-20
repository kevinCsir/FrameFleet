#include "framefleet_engine/engine.hpp"

#include <chrono>
#include <filesystem>
#include <fstream>
#include <random>
#include <stdexcept>
#include <thread>

namespace framefleet_engine {
namespace {

std::int64_t file_size_or_zero(const std::string& path) {
    std::error_code ec;
    const auto size = std::filesystem::file_size(path, ec);
    if (ec) {
        return 0;
    }
    return static_cast<std::int64_t>(size);
}

void ensure_parent_dir(const std::string& path) {
    const auto parent = std::filesystem::path(path).parent_path();
    if (!parent.empty()) {
        std::filesystem::create_directories(parent);
    }
}

void copy_file(const std::string& input_path, const std::string& output_path) {
    ensure_parent_dir(output_path);
    std::ifstream input(input_path, std::ios::binary);
    if (!input) {
        throw std::runtime_error("open input failed: " + input_path);
    }

    std::ofstream output(output_path, std::ios::binary | std::ios::trunc);
    if (!output) {
        throw std::runtime_error("open output failed: " + output_path);
    }

    output << input.rdbuf();
    if (!output) {
        throw std::runtime_error("write output failed: " + output_path);
    }
}

void copy_file_range(const std::string& input_path, const std::string& output_path, std::int64_t offset, std::int64_t length) {
    ensure_parent_dir(output_path);
    std::ifstream input(input_path, std::ios::binary);
    if (!input) {
        throw std::runtime_error("open input failed: " + input_path);
    }

    std::ofstream output(output_path, std::ios::binary | std::ios::trunc);
    if (!output) {
        throw std::runtime_error("open output failed: " + output_path);
    }

    input.seekg(offset, std::ios::beg);
    if (!input && length > 0) {
        throw std::runtime_error("seek input failed: " + input_path);
    }

    constexpr std::size_t buffer_size = 64 * 1024;
    char buffer[buffer_size];
    std::int64_t remaining = length;
    while (remaining > 0) {
        const auto want = static_cast<std::streamsize>(std::min<std::int64_t>(remaining, buffer_size));
        input.read(buffer, want);
        const auto got = input.gcount();
        if (got <= 0) {
            throw std::runtime_error("read input range failed: " + input_path);
        }
        output.write(buffer, got);
        if (!output) {
            throw std::runtime_error("write output failed: " + output_path);
        }
        remaining -= got;
    }
}

void append_file(const std::string& input_path, std::ofstream& output) {
    std::ifstream input(input_path, std::ios::binary);
    if (!input) {
        throw std::runtime_error("open input failed: " + input_path);
    }

    output << input.rdbuf();
    if (!output) {
        throw std::runtime_error("append output failed");
    }
}

std::int64_t elapsed_ms(std::chrono::steady_clock::time_point start) {
    const auto elapsed = std::chrono::steady_clock::now() - start;
    return std::chrono::duration_cast<std::chrono::milliseconds>(elapsed).count();
}

void wait_for_fake_work() {
    thread_local std::mt19937 rng(std::random_device{}());
    std::uniform_int_distribution<int> delay_ms(200, 400);
    std::this_thread::sleep_for(std::chrono::milliseconds(delay_ms(rng)));
}

Response handle_process_internal_simple(const Request& request) {
    const auto start = std::chrono::steady_clock::now();
    wait_for_fake_work();
    copy_file(request.input->path, request.output->path);

    auto response = make_completed_response(request);
    response.result_name = request.output->name.empty() ? basename_from_path(request.output->path) : request.output->name;
    response.duration_ms = elapsed_ms(start);
    response.output_size_bytes = file_size_or_zero(request.output->path);
    return response;
}

Response handle_split_video(const Request& request) {
    const auto start = std::chrono::steady_clock::now();
    wait_for_fake_work();
    std::filesystem::create_directories(request.output_dir);

    const auto input_size = file_size_or_zero(request.input->path);
    const auto segment_count = request.segment_count;

    auto response = make_completed_response(request);
    std::int64_t offset = 0;
    for (int index = 0; index < segment_count; ++index) {
        const auto name = "segment_" + std::to_string(index) + ".mp4";
        const auto path = (std::filesystem::path(request.output_dir) / name).string();
        const auto remaining_segments = segment_count - index;
        const auto remaining_bytes = input_size - offset;
        const auto length = remaining_segments > 0 ? (remaining_bytes + remaining_segments - 1) / remaining_segments : 0;
        copy_file_range(request.input->path, path, offset, length);
        offset += length;
        response.segments.push_back(SegmentFile{
            index,
            path,
            name,
            file_size_or_zero(path),
        });
    }
    response.duration_ms = elapsed_ms(start);
    return response;
}

Response handle_process_segment(const Request& request) {
    const auto start = std::chrono::steady_clock::now();
    wait_for_fake_work();
    copy_file(request.input->path, request.output->path);

    auto response = make_completed_response(request);
    response.artifact_name = request.output->name.empty() ? basename_from_path(request.output->path) : request.output->name;
    response.duration_ms = elapsed_ms(start);
    response.output_size_bytes = file_size_or_zero(request.output->path);
    return response;
}

Response handle_assemble_gif(const Request& request) {
    const auto start = std::chrono::steady_clock::now();
    wait_for_fake_work();
    ensure_parent_dir(request.output->path);

    std::ofstream output(request.output->path, std::ios::binary | std::ios::trunc);
    if (!output) {
        throw std::runtime_error("open output failed: " + request.output->path);
    }
    for (const auto& input : request.inputs) {
        append_file(input.path, output);
    }
    output.close();
    if (!output) {
        throw std::runtime_error("close output failed: " + request.output->path);
    }

    auto response = make_completed_response(request);
    response.result_name = request.output->name.empty() ? basename_from_path(request.output->path) : request.output->name;
    response.duration_ms = elapsed_ms(start);
    response.output_size_bytes = file_size_or_zero(request.output->path);
    return response;
}

}  // namespace

Response handle_request(const Request& request) {
    try {
        if (request.op == "ping") {
            return make_completed_response(request);
        }
        if (request.op == "process_internal_simple") {
            return handle_process_internal_simple(request);
        }
        if (request.op == "split_video") {
            return handle_split_video(request);
        }
        if (request.op == "process_segment") {
            return handle_process_segment(request);
        }
        if (request.op == "assemble_gif") {
            return handle_assemble_gif(request);
        }
        return make_failed_response(request, "unknown op: " + request.op, false);
    } catch (const std::exception& err) {
        return make_failed_response(request, err.what(), true);
    }
}

}  // namespace framefleet_engine
