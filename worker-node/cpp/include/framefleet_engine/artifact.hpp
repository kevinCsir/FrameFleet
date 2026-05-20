#pragma once

#include <cstdint>
#include <fstream>
#include <string>
#include <vector>

namespace framefleet_engine {

constexpr std::uint16_t kArtifactVersion = 1;
constexpr std::uint16_t kArtifactHeaderSize = 64;
constexpr std::uint32_t kArtifactCodecPngBGRA = 1;
constexpr std::uint64_t kArtifactMaxPayloadBytes = 256ULL * 1024ULL * 1024ULL;

struct ArtifactMetadata {
    std::uint32_t flags = 0;
    std::uint32_t codec = kArtifactCodecPngBGRA;
    std::uint32_t width = 0;
    std::uint32_t height = 0;
    std::uint32_t fps_num = 0;
    std::uint32_t fps_den = 0;
    std::uint32_t frame_count = 0;
    std::uint32_t segment_index = 0;
    std::uint64_t duration_ms = 0;
};

struct ArtifactFrame {
    std::uint32_t frame_index = 0;
    std::uint32_t duration_ms = 0;
    std::vector<std::uint8_t> payload;
};

class ArtifactWriter {
public:
    ArtifactWriter(const std::string& path, ArtifactMetadata metadata);

    ArtifactWriter(const ArtifactWriter&) = delete;
    ArtifactWriter& operator=(const ArtifactWriter&) = delete;

    void write_frame(std::uint32_t frame_index,
                     std::uint32_t duration_ms,
                     const std::vector<std::uint8_t>& payload);
    void close();

private:
    std::ofstream output_;
    ArtifactMetadata metadata_;
    std::uint32_t written_frames_ = 0;
    bool closed_ = false;
};

class ArtifactReader {
public:
    explicit ArtifactReader(const std::string& path);

    ArtifactReader(const ArtifactReader&) = delete;
    ArtifactReader& operator=(const ArtifactReader&) = delete;

    const ArtifactMetadata& metadata() const;
    bool next_frame(ArtifactFrame& frame);

private:
    std::ifstream input_;
    ArtifactMetadata metadata_;
    std::uint32_t read_frames_ = 0;
};

}  // namespace framefleet_engine
