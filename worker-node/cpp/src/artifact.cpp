#include "framefleet_engine/artifact.hpp"

#include <array>
#include <filesystem>
#include <stdexcept>

namespace framefleet_engine {
namespace {

void ensure_parent_dir(const std::string& path) {
    const auto parent = std::filesystem::path(path).parent_path();
    if (!parent.empty()) {
        std::filesystem::create_directories(parent);
    }
}

void validate_metadata(const ArtifactMetadata& metadata) {
    if (metadata.codec != kArtifactCodecPngBGRA) {
        throw std::runtime_error("unsupported artifact codec");
    }
    if (metadata.width == 0 || metadata.height == 0) {
        throw std::runtime_error("artifact dimensions are required");
    }
    if (metadata.fps_num == 0 || metadata.fps_den == 0) {
        throw std::runtime_error("artifact fps is required");
    }
    if (metadata.frame_count == 0) {
        throw std::runtime_error("artifact frame_count is required");
    }
}

void write_exact(std::ofstream& output, const char* data, std::size_t size) {
    output.write(data, static_cast<std::streamsize>(size));
    if (!output) {
        throw std::runtime_error("write artifact failed");
    }
}

void read_exact(std::ifstream& input, char* data, std::size_t size) {
    input.read(data, static_cast<std::streamsize>(size));
    if (input.gcount() != static_cast<std::streamsize>(size)) {
        throw std::runtime_error("truncated artifact");
    }
}

void write_u16_le(std::ofstream& output, std::uint16_t value) {
    std::array<char, 2> bytes{
        static_cast<char>(value & 0xff),
        static_cast<char>((value >> 8) & 0xff),
    };
    write_exact(output, bytes.data(), bytes.size());
}

void write_u32_le(std::ofstream& output, std::uint32_t value) {
    std::array<char, 4> bytes{
        static_cast<char>(value & 0xff),
        static_cast<char>((value >> 8) & 0xff),
        static_cast<char>((value >> 16) & 0xff),
        static_cast<char>((value >> 24) & 0xff),
    };
    write_exact(output, bytes.data(), bytes.size());
}

void write_u64_le(std::ofstream& output, std::uint64_t value) {
    std::array<char, 8> bytes{
        static_cast<char>(value & 0xff),
        static_cast<char>((value >> 8) & 0xff),
        static_cast<char>((value >> 16) & 0xff),
        static_cast<char>((value >> 24) & 0xff),
        static_cast<char>((value >> 32) & 0xff),
        static_cast<char>((value >> 40) & 0xff),
        static_cast<char>((value >> 48) & 0xff),
        static_cast<char>((value >> 56) & 0xff),
    };
    write_exact(output, bytes.data(), bytes.size());
}

std::uint16_t read_u16_le(std::ifstream& input) {
    std::array<unsigned char, 2> bytes{};
    read_exact(input, reinterpret_cast<char*>(bytes.data()), bytes.size());
    return static_cast<std::uint16_t>(bytes[0]) |
           (static_cast<std::uint16_t>(bytes[1]) << 8);
}

std::uint32_t read_u32_le(std::ifstream& input) {
    std::array<unsigned char, 4> bytes{};
    read_exact(input, reinterpret_cast<char*>(bytes.data()), bytes.size());
    return static_cast<std::uint32_t>(bytes[0]) |
           (static_cast<std::uint32_t>(bytes[1]) << 8) |
           (static_cast<std::uint32_t>(bytes[2]) << 16) |
           (static_cast<std::uint32_t>(bytes[3]) << 24);
}

std::uint64_t read_u64_le(std::ifstream& input) {
    std::array<unsigned char, 8> bytes{};
    read_exact(input, reinterpret_cast<char*>(bytes.data()), bytes.size());
    return static_cast<std::uint64_t>(bytes[0]) |
           (static_cast<std::uint64_t>(bytes[1]) << 8) |
           (static_cast<std::uint64_t>(bytes[2]) << 16) |
           (static_cast<std::uint64_t>(bytes[3]) << 24) |
           (static_cast<std::uint64_t>(bytes[4]) << 32) |
           (static_cast<std::uint64_t>(bytes[5]) << 40) |
           (static_cast<std::uint64_t>(bytes[6]) << 48) |
           (static_cast<std::uint64_t>(bytes[7]) << 56);
}

}  // namespace

ArtifactWriter::ArtifactWriter(const std::string& path, ArtifactMetadata metadata)
    : metadata_(metadata) {
    validate_metadata(metadata_);
    ensure_parent_dir(path);
    output_.open(path, std::ios::binary | std::ios::trunc);
    if (!output_) {
        throw std::runtime_error("open artifact for write failed: " + path);
    }

    write_exact(output_, "FFAF", 4);
    write_u16_le(output_, kArtifactVersion);
    write_u16_le(output_, kArtifactHeaderSize);
    write_u32_le(output_, metadata_.flags);
    write_u32_le(output_, metadata_.codec);
    write_u32_le(output_, metadata_.width);
    write_u32_le(output_, metadata_.height);
    write_u32_le(output_, metadata_.fps_num);
    write_u32_le(output_, metadata_.fps_den);
    write_u32_le(output_, metadata_.frame_count);
    write_u32_le(output_, metadata_.segment_index);
    write_u64_le(output_, metadata_.duration_ms);
    write_u64_le(output_, 0);
    write_u64_le(output_, 0);
}

void ArtifactWriter::write_frame(std::uint32_t frame_index,
                                 std::uint32_t duration_ms,
                                 const std::vector<std::uint8_t>& payload) {
    if (closed_) {
        throw std::runtime_error("artifact writer is closed");
    }
    if (written_frames_ >= metadata_.frame_count) {
        throw std::runtime_error("too many artifact frames");
    }
    if (payload.empty()) {
        throw std::runtime_error("artifact frame payload is empty");
    }
    if (payload.size() > kArtifactMaxPayloadBytes) {
        throw std::runtime_error("artifact frame payload is too large");
    }

    write_u32_le(output_, frame_index);
    write_u32_le(output_, duration_ms);
    write_u64_le(output_, static_cast<std::uint64_t>(payload.size()));
    write_exact(output_, reinterpret_cast<const char*>(payload.data()), payload.size());
    written_frames_++;
}

void ArtifactWriter::close() {
    if (closed_) {
        return;
    }
    if (written_frames_ != metadata_.frame_count) {
        throw std::runtime_error("artifact frame count mismatch");
    }
    output_.close();
    if (!output_) {
        throw std::runtime_error("close artifact failed");
    }
    closed_ = true;
}

ArtifactReader::ArtifactReader(const std::string& path) {
    input_.open(path, std::ios::binary);
    if (!input_) {
        throw std::runtime_error("open artifact for read failed: " + path);
    }

    std::array<char, 4> magic{};
    read_exact(input_, magic.data(), magic.size());
    if (std::string(magic.data(), magic.size()) != "FFAF") {
        throw std::runtime_error("invalid artifact magic");
    }

    const auto version = read_u16_le(input_);
    if (version != kArtifactVersion) {
        throw std::runtime_error("unsupported artifact version");
    }

    const auto header_size = read_u16_le(input_);
    if (header_size < kArtifactHeaderSize) {
        throw std::runtime_error("invalid artifact header size");
    }

    metadata_.flags = read_u32_le(input_);
    metadata_.codec = read_u32_le(input_);
    metadata_.width = read_u32_le(input_);
    metadata_.height = read_u32_le(input_);
    metadata_.fps_num = read_u32_le(input_);
    metadata_.fps_den = read_u32_le(input_);
    metadata_.frame_count = read_u32_le(input_);
    metadata_.segment_index = read_u32_le(input_);
    metadata_.duration_ms = read_u64_le(input_);
    (void)read_u64_le(input_);
    (void)read_u64_le(input_);

    if (header_size > kArtifactHeaderSize) {
        input_.ignore(static_cast<std::streamsize>(header_size - kArtifactHeaderSize));
        if (!input_) {
            throw std::runtime_error("truncated artifact extension header");
        }
    }

    validate_metadata(metadata_);
}

const ArtifactMetadata& ArtifactReader::metadata() const {
    return metadata_;
}

bool ArtifactReader::next_frame(ArtifactFrame& frame) {
    if (read_frames_ >= metadata_.frame_count) {
        return false;
    }

    frame.frame_index = read_u32_le(input_);
    frame.duration_ms = read_u32_le(input_);
    const auto payload_size = read_u64_le(input_);
    if (payload_size == 0) {
        throw std::runtime_error("artifact frame payload is empty");
    }
    if (payload_size > kArtifactMaxPayloadBytes) {
        throw std::runtime_error("artifact frame payload is too large");
    }

    frame.payload.resize(static_cast<std::size_t>(payload_size));
    read_exact(input_, reinterpret_cast<char*>(frame.payload.data()), frame.payload.size());
    read_frames_++;
    return true;
}

}  // namespace framefleet_engine
