#include "framefleet_engine/engine.hpp"
#include "framefleet_engine/artifact.hpp"

#include <chrono>
#include <cerrno>
#include <cmath>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <sstream>
#include <random>
#include <stdexcept>
#include <string>
#include <thread>
#include <algorithm>
#include <vector>

#include <opencv2/core.hpp>
#include <opencv2/imgcodecs.hpp>
#include <opencv2/imgproc.hpp>
#include <opencv2/videoio.hpp>

#include <sys/wait.h>
#include <system_error>
#include <unistd.h>

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

bool env_enabled(const char* name) {
    const char* value = std::getenv(name);
    if (value == nullptr) {
        return false;
    }
    return std::string(value) == "1" || std::string(value) == "true" || std::string(value) == "TRUE";
}

std::string env_or_default(const char* name, const std::string& fallback) {
    const char* value = std::getenv(name);
    if (value == nullptr || std::string(value).empty()) {
        return fallback;
    }
    return value;
}

int env_int_or_default(const char* name, int fallback) {
    const char* value = std::getenv(name);
    if (value == nullptr || std::string(value).empty()) {
        return fallback;
    }
    try {
        const auto parsed = std::stoi(value);
        if (parsed > 0) {
            return parsed;
        }
    } catch (const std::exception&) {
    }
    return fallback;
}

std::string quote_command(const std::vector<std::string>& argv) {
    std::ostringstream out;
    for (std::size_t i = 0; i < argv.size(); ++i) {
        if (i > 0) {
            out << ' ';
        }
        out << argv[i];
    }
    return out.str();
}

void run_process(const std::vector<std::string>& argv) {
    if (argv.empty()) {
        throw std::runtime_error("empty command");
    }

    pid_t pid = fork();
    if (pid < 0) {
        throw std::runtime_error("fork failed: " + std::string(std::strerror(errno)));
    }
    if (pid == 0) {
        std::vector<char*> args;
        args.reserve(argv.size() + 1);
        for (const auto& arg : argv) {
            args.push_back(const_cast<char*>(arg.c_str()));
        }
        args.push_back(nullptr);

        setenv("OMP_NUM_THREADS", "1", 1);
        setenv("OPENBLAS_NUM_THREADS", "1", 1);
        setenv("MKL_NUM_THREADS", "1", 1);
        setenv("VECLIB_MAXIMUM_THREADS", "1", 1);
        setenv("NUMEXPR_NUM_THREADS", "1", 1);

        execvp(args[0], args.data());
        _exit(127);
    }

    int status = 0;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno == EINTR) {
            continue;
        }
        throw std::runtime_error("waitpid failed: " + std::string(std::strerror(errno)));
    }

    if (WIFEXITED(status) && WEXITSTATUS(status) == 0) {
        return;
    }
    if (WIFEXITED(status)) {
        throw std::runtime_error("command failed with exit code " + std::to_string(WEXITSTATUS(status)) + ": " + quote_command(argv));
    }
    if (WIFSIGNALED(status)) {
        throw std::runtime_error("command killed by signal " + std::to_string(WTERMSIG(status)) + ": " + quote_command(argv));
    }
    throw std::runtime_error("command failed: " + quote_command(argv));
}

std::string read_process_stdout(const std::vector<std::string>& argv) {
    if (argv.empty()) {
        throw std::runtime_error("empty command");
    }

    int pipefd[2];
    if (pipe(pipefd) < 0) {
        throw std::runtime_error("pipe failed: " + std::string(std::strerror(errno)));
    }

    pid_t pid = fork();
    if (pid < 0) {
        close(pipefd[0]);
        close(pipefd[1]);
        throw std::runtime_error("fork failed: " + std::string(std::strerror(errno)));
    }
    if (pid == 0) {
        close(pipefd[0]);
        dup2(pipefd[1], STDOUT_FILENO);
        close(pipefd[1]);

        std::vector<char*> args;
        args.reserve(argv.size() + 1);
        for (const auto& arg : argv) {
            args.push_back(const_cast<char*>(arg.c_str()));
        }
        args.push_back(nullptr);

        execvp(args[0], args.data());
        _exit(127);
    }

    close(pipefd[1]);
    std::string output;
    char buffer[4096];
    while (true) {
        const ssize_t n = read(pipefd[0], buffer, sizeof(buffer));
        if (n > 0) {
            output.append(buffer, static_cast<std::size_t>(n));
            continue;
        }
        if (n == 0) {
            break;
        }
        if (errno == EINTR) {
            continue;
        }
        close(pipefd[0]);
        throw std::runtime_error("read command output failed: " + std::string(std::strerror(errno)));
    }
    close(pipefd[0]);

    int status = 0;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno == EINTR) {
            continue;
        }
        throw std::runtime_error("waitpid failed: " + std::string(std::strerror(errno)));
    }
    if (!WIFEXITED(status) || WEXITSTATUS(status) != 0) {
        throw std::runtime_error("command failed: " + quote_command(argv));
    }
    return output;
}

double probe_duration_seconds(const std::string& input_path) {
    const auto ffprobe = env_or_default("FRAMEFLEET_FFPROBE_PATH", "ffprobe");
    const auto output = read_process_stdout({
        ffprobe,
        "-v", "error",
        "-show_entries", "format=duration",
        "-of", "default=noprint_wrappers=1:nokey=1",
        input_path,
    });

    try {
        const auto duration = std::stod(output);
        if (std::isfinite(duration) && duration > 0) {
            return duration;
        }
    } catch (const std::exception&) {
    }
    throw std::runtime_error("ffprobe returned invalid duration for input: " + input_path);
}

int split_count(const Request& request, double duration_seconds, std::int64_t input_size_bytes) {
    int count = 1;

    if (request.target_segment_duration_ms > 0) {
        const double target_seconds = static_cast<double>(request.target_segment_duration_ms) / 1000.0;
        if (target_seconds > 0) {
            count = std::max(count, static_cast<int>(std::ceil(duration_seconds / target_seconds)));
        }
    }

    if (request.target_segment_size_bytes > 0 && input_size_bytes > 0) {
        const auto by_size = static_cast<int>(std::ceil(
            static_cast<double>(input_size_bytes) / static_cast<double>(request.target_segment_size_bytes)));
        count = std::max(count, by_size);
    }

    if (request.max_segments > 0) {
        count = std::min(count, request.max_segments);
    }
    return std::max(1, count);
}

void split_segment_with_ffmpeg(const Request& request, int index, double start_seconds, double duration_seconds, const std::string& output_path) {
    const auto ffmpeg = env_or_default("FRAMEFLEET_FFMPEG_PATH", "ffmpeg");
    run_process({
        ffmpeg,
        "-hide_banner",
        "-v", "error",
        "-nostdin",
        "-y",
        "-threads", "1",
        "-filter_threads", "1",
        "-filter_complex_threads", "1",
        "-ss", std::to_string(start_seconds),
        "-t", std::to_string(duration_seconds),
        "-i", request.input->path,
        "-map", "0",
        "-c", "copy",
        "-avoid_negative_ts", "make_zero",
        output_path,
    });
}

std::string segment_pattern(const std::string& output_dir) {
    return (std::filesystem::path(output_dir) / "segment_%d.mp4").string();
}

void split_video_with_ffmpeg_segment_muxer(const Request& request, double segment_time_seconds) {
    const auto ffmpeg = env_or_default("FRAMEFLEET_FFMPEG_PATH", "ffmpeg");
    run_process({
        ffmpeg,
        "-hide_banner",
        "-v", "error",
        "-nostdin",
        "-y",
        "-threads", "1",
        "-filter_threads", "1",
        "-filter_complex_threads", "1",
        "-i", request.input->path,
        "-map", "0",
        "-c", "copy",
        "-f", "segment",
        "-segment_time", std::to_string(segment_time_seconds),
        "-reset_timestamps", "1",
        "-avoid_negative_ts", "make_zero",
        segment_pattern(request.output_dir),
    });
}

std::vector<SegmentFile> collect_split_segments(const std::string& output_dir) {
    std::vector<SegmentFile> segments;
    for (const auto& entry : std::filesystem::directory_iterator(output_dir)) {
        if (!entry.is_regular_file()) {
            continue;
        }
        const auto path = entry.path();
        const auto name = path.filename().string();
        const std::string prefix = "segment_";
        const std::string suffix = ".mp4";
        if (name.rfind(prefix, 0) != 0 || name.size() <= prefix.size() + suffix.size()) {
            continue;
        }
        if (name.substr(name.size() - suffix.size()) != suffix) {
            continue;
        }
        const auto index_text = name.substr(prefix.size(), name.size() - prefix.size() - suffix.size());
        try {
            std::size_t consumed = 0;
            const auto index = std::stoi(index_text, &consumed);
            if (consumed != index_text.size() || index < 0) {
                continue;
            }
            segments.push_back(SegmentFile{
                index,
                path.string(),
                name,
                file_size_or_zero(path.string()),
            });
        } catch (const std::exception&) {
        }
    }

    std::sort(segments.begin(), segments.end(), [](const SegmentFile& left, const SegmentFile& right) {
        return left.segment_index < right.segment_index;
    });
    for (int index = 0; index < static_cast<int>(segments.size()); ++index) {
        if (segments[index].segment_index != index) {
            throw std::runtime_error("ffmpeg segment output has non-contiguous segment indexes");
        }
    }
    return segments;
}

std::filesystem::path make_temp_dir(const std::string& prefix) {
    auto pattern = (std::filesystem::temp_directory_path() / (prefix + "XXXXXX")).string();
    std::vector<char> buffer(pattern.begin(), pattern.end());
    buffer.push_back('\0');
    char* path = mkdtemp(buffer.data());
    if (path == nullptr) {
        throw std::runtime_error("mkdtemp failed: " + std::string(std::strerror(errno)));
    }
    return std::filesystem::path(path);
}

class TempDir {
public:
    explicit TempDir(std::filesystem::path path) : path_(std::move(path)) {}
    ~TempDir() {
        std::error_code ec;
        std::filesystem::remove_all(path_, ec);
    }

    const std::filesystem::path& path() const {
        return path_;
    }

private:
    std::filesystem::path path_;
};

void write_binary_file(const std::filesystem::path& path, const std::vector<std::uint8_t>& data) {
    ensure_parent_dir(path.string());
    std::ofstream output(path, std::ios::binary | std::ios::trunc);
    if (!output) {
        throw std::runtime_error("open binary output failed: " + path.string());
    }
    output.write(reinterpret_cast<const char*>(data.data()), static_cast<std::streamsize>(data.size()));
    if (!output) {
        throw std::runtime_error("write binary output failed: " + path.string());
    }
}

std::string frame_file_name(std::uint64_t index) {
    std::ostringstream out;
    out << "frame_" << std::setw(8) << std::setfill('0') << index << ".png";
    return out.str();
}

void assemble_gif_with_ffmpeg(const std::filesystem::path& frames_pattern,
                              double fps,
                              const std::string& output_path) {
    const auto ffmpeg = env_or_default("FRAMEFLEET_FFMPEG_PATH", "ffmpeg");
    run_process({
        ffmpeg,
        "-hide_banner",
        "-v", "error",
        "-nostdin",
        "-y",
        "-threads", "1",
        "-filter_threads", "1",
        "-filter_complex_threads", "1",
        "-framerate", std::to_string(fps),
        "-i", frames_pattern.string(),
        "-filter_complex", "split[s0][s1];[s0]palettegen=reserve_transparent=1:transparency_color=000000[p];[s1][p]paletteuse=alpha_threshold=128",
        "-plays", "0",
        output_path,
    });
}

double capture_fps(cv::VideoCapture& capture) {
    const auto fps = capture.get(cv::CAP_PROP_FPS);
    if (std::isfinite(fps) && fps > 0) {
        return fps;
    }
    return 12.0;
}

int count_video_frames(const std::string& path) {
    cv::VideoCapture capture(path);
    if (!capture.isOpened()) {
        throw std::runtime_error("open segment video failed: " + path);
    }

    int count = 0;
    cv::Mat frame;
    while (capture.read(frame)) {
        count++;
    }
    return count;
}

std::uint32_t frame_duration_ms(double fps) {
    if (!std::isfinite(fps) || fps <= 0) {
        return 83;
    }
    return static_cast<std::uint32_t>(std::max(1.0, std::round(1000.0 / fps)));
}

std::vector<std::uint8_t> encode_red_edge_png(const cv::Mat& frame) {
    cv::Mat gray;
    cv::cvtColor(frame, gray, cv::COLOR_BGR2GRAY);

    const auto low_threshold = env_int_or_default("FRAMEFLEET_CANNY_LOW_THRESHOLD", 80);
    const auto high_threshold = env_int_or_default("FRAMEFLEET_CANNY_HIGH_THRESHOLD", 160);

    cv::Mat edges;
    cv::Canny(gray, edges, low_threshold, high_threshold);

    std::vector<cv::Mat> channels(4);
    channels[0] = cv::Mat(edges.size(), CV_8UC1, cv::Scalar(0));
    channels[1] = cv::Mat(edges.size(), CV_8UC1, cv::Scalar(0));
    channels[2] = edges;
    channels[3] = edges;

    cv::Mat bgra;
    cv::merge(channels, bgra);

    std::vector<std::uint8_t> encoded;
    if (!cv::imencode(".png", bgra, encoded)) {
        throw std::runtime_error("encode edge png failed");
    }
    return encoded;
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
    std::filesystem::create_directories(request.output_dir);

    if (env_enabled("FRAMEFLEET_ENGINE_FAKE_SPLIT")) {
        wait_for_fake_work();
        const auto input_size = file_size_or_zero(request.input->path);
        int fake_segments = request.max_segments > 0 ? request.max_segments : 1;
        if (request.max_segments <= 0 && request.target_segment_size_bytes > 0 && input_size > 0) {
            fake_segments = std::max(1, static_cast<int>(std::ceil(
                static_cast<double>(input_size) / static_cast<double>(request.target_segment_size_bytes))));
        }

        auto response = make_completed_response(request);
        std::int64_t offset = 0;
        for (int index = 0; index < fake_segments; ++index) {
            const auto name = "segment_" + std::to_string(index) + ".mp4";
            const auto path = (std::filesystem::path(request.output_dir) / name).string();
            const auto remaining_segments = fake_segments - index;
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

    const auto video_duration_seconds = probe_duration_seconds(request.input->path);
    const auto input_size_bytes = file_size_or_zero(request.input->path);
    const auto count = split_count(request, video_duration_seconds, input_size_bytes);
    const auto segment_duration_seconds = video_duration_seconds / static_cast<double>(count);

    auto response = make_completed_response(request);
    if (request.max_segments <= 0) {
        split_video_with_ffmpeg_segment_muxer(request, segment_duration_seconds);
        response.segments = collect_split_segments(request.output_dir);
        response.duration_ms = elapsed_ms(start);
        return response;
    }

    for (int index = 0; index < count; ++index) {
        const auto name = "segment_" + std::to_string(index) + ".mp4";
        const auto path = (std::filesystem::path(request.output_dir) / name).string();
        const auto start_seconds = segment_duration_seconds * static_cast<double>(index);
        const auto duration_seconds = index == count - 1 ? video_duration_seconds - start_seconds : segment_duration_seconds;
        split_segment_with_ffmpeg(request, index, start_seconds, duration_seconds, path);
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
    if (env_enabled("FRAMEFLEET_ENGINE_FAKE_PROCESS")) {
        wait_for_fake_work();
        copy_file(request.input->path, request.output->path);

        auto response = make_completed_response(request);
        response.artifact_name = request.output->name.empty() ? basename_from_path(request.output->path) : request.output->name;
        response.duration_ms = elapsed_ms(start);
        response.output_size_bytes = file_size_or_zero(request.output->path);
        return response;
    }

    cv::setNumThreads(1);
    cv::VideoCapture capture(request.input->path);
    if (!capture.isOpened()) {
        throw std::runtime_error("open segment video failed: " + request.input->path);
    }

    const auto fps = capture_fps(capture);
    const auto frame_duration = frame_duration_ms(fps);
    auto expected_frame_count = static_cast<int>(capture.get(cv::CAP_PROP_FRAME_COUNT));
    if (expected_frame_count <= 0) {
        expected_frame_count = count_video_frames(request.input->path);
        capture.release();
        capture.open(request.input->path);
        if (!capture.isOpened()) {
            throw std::runtime_error("reopen segment video failed: " + request.input->path);
        }
    }
    if (expected_frame_count <= 0) {
        throw std::runtime_error("segment video has no frames: " + request.input->path);
    }

    const auto width = static_cast<std::uint32_t>(std::max(1.0, capture.get(cv::CAP_PROP_FRAME_WIDTH)));
    const auto height = static_cast<std::uint32_t>(std::max(1.0, capture.get(cv::CAP_PROP_FRAME_HEIGHT)));

    ArtifactMetadata metadata;
    metadata.codec = kArtifactCodecPngBGRA;
    metadata.width = width;
    metadata.height = height;
    metadata.fps_num = static_cast<std::uint32_t>(std::max(1.0, std::round(fps * 1000.0)));
    metadata.fps_den = 1000;
    metadata.frame_count = static_cast<std::uint32_t>(expected_frame_count);
    metadata.segment_index = 0;
    metadata.duration_ms = static_cast<std::uint64_t>(metadata.frame_count) * frame_duration;

    ArtifactWriter writer(request.output->path, metadata);
    cv::Mat frame;
    std::uint32_t frame_index = 0;
    while (capture.read(frame)) {
        if (frame.empty()) {
            continue;
        }
        if (frame_index >= metadata.frame_count) {
            break;
        }
        const auto encoded = encode_red_edge_png(frame);
        writer.write_frame(frame_index, frame_duration, encoded);
        frame_index++;
    }

    if (frame_index != metadata.frame_count) {
        throw std::runtime_error("segment frame count changed while processing");
    }
    writer.close();

    auto response = make_completed_response(request);
    response.artifact_name = request.output->name.empty() ? basename_from_path(request.output->path) : request.output->name;
    response.duration_ms = elapsed_ms(start);
    response.output_size_bytes = file_size_or_zero(request.output->path);
    response.frame_count = static_cast<int>(metadata.frame_count);
    return response;
}

Response handle_assemble_gif(const Request& request) {
    const auto start = std::chrono::steady_clock::now();
    if (env_enabled("FRAMEFLEET_ENGINE_FAKE_ASSEMBLE")) {
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

    ensure_parent_dir(request.output->path);

    TempDir temp(make_temp_dir("framefleet_assemble_"));
    std::uint64_t output_frame_index = 0;
    double fps = 0;
    std::uint32_t width = 0;
    std::uint32_t height = 0;

    for (const auto& input : request.inputs) {
        ArtifactReader reader(input.path);
        const auto& metadata = reader.metadata();
        if (metadata.codec != kArtifactCodecPngBGRA) {
            throw std::runtime_error("unsupported artifact codec while assembling");
        }
        if (width == 0 && height == 0) {
            width = metadata.width;
            height = metadata.height;
            fps = static_cast<double>(metadata.fps_num) / static_cast<double>(metadata.fps_den);
        } else if (width != metadata.width || height != metadata.height) {
            throw std::runtime_error("artifact dimensions differ while assembling");
        }

        ArtifactFrame frame;
        while (reader.next_frame(frame)) {
            const auto frame_path = temp.path() / frame_file_name(output_frame_index);
            write_binary_file(frame_path, frame.payload);
            output_frame_index++;
        }
    }

    if (output_frame_index == 0) {
        throw std::runtime_error("assemble_gif received no artifact frames");
    }
    if (!std::isfinite(fps) || fps <= 0) {
        fps = 12.0;
    }

    assemble_gif_with_ffmpeg(temp.path() / "frame_%08d.png", fps, request.output->path);

    auto response = make_completed_response(request);
    response.result_name = request.output->name.empty() ? basename_from_path(request.output->path) : request.output->name;
    response.duration_ms = elapsed_ms(start);
    response.output_size_bytes = file_size_or_zero(request.output->path);
    response.frame_count = static_cast<int>(output_frame_index);
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
