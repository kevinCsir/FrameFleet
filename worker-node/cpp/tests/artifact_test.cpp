#include "framefleet_engine/artifact.hpp"

#include <filesystem>
#include <fstream>
#include <iostream>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

using framefleet_engine::ArtifactFrame;
using framefleet_engine::ArtifactMetadata;
using framefleet_engine::ArtifactReader;
using framefleet_engine::ArtifactWriter;
using framefleet_engine::kArtifactCodecPngBGRA;

void require(bool condition, const std::string& message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

std::filesystem::path temp_root() {
    auto root = std::filesystem::temp_directory_path() / "framefleet_artifact_test";
    std::filesystem::remove_all(root);
    std::filesystem::create_directories(root);
    return root;
}

void write_bytes(const std::filesystem::path& path, const std::vector<unsigned char>& bytes) {
    std::ofstream output(path, std::ios::binary | std::ios::trunc);
    require(static_cast<bool>(output), "open test file failed");
    output.write(reinterpret_cast<const char*>(bytes.data()), static_cast<std::streamsize>(bytes.size()));
    require(static_cast<bool>(output), "write test file failed");
}

void write_u16_le(std::vector<unsigned char>& out, std::uint16_t value) {
    out.push_back(static_cast<unsigned char>(value & 0xff));
    out.push_back(static_cast<unsigned char>((value >> 8) & 0xff));
}

void write_u32_le(std::vector<unsigned char>& out, std::uint32_t value) {
    out.push_back(static_cast<unsigned char>(value & 0xff));
    out.push_back(static_cast<unsigned char>((value >> 8) & 0xff));
    out.push_back(static_cast<unsigned char>((value >> 16) & 0xff));
    out.push_back(static_cast<unsigned char>((value >> 24) & 0xff));
}

void write_u64_le(std::vector<unsigned char>& out, std::uint64_t value) {
    for (int shift = 0; shift < 64; shift += 8) {
        out.push_back(static_cast<unsigned char>((value >> shift) & 0xff));
    }
}

std::vector<unsigned char> valid_header_with_version(std::uint16_t version) {
    std::vector<unsigned char> bytes{'F', 'F', 'A', 'F'};
    write_u16_le(bytes, version);
    write_u16_le(bytes, framefleet_engine::kArtifactHeaderSize);
    write_u32_le(bytes, 0);
    write_u32_le(bytes, kArtifactCodecPngBGRA);
    write_u32_le(bytes, 10);
    write_u32_le(bytes, 10);
    write_u32_le(bytes, 1);
    write_u32_le(bytes, 1);
    write_u32_le(bytes, 1);
    write_u32_le(bytes, 0);
    write_u64_le(bytes, 1000);
    write_u64_le(bytes, 0);
    write_u64_le(bytes, 0);
    return bytes;
}

void expect_reader_error(const std::filesystem::path& path, const std::string& label) {
    try {
        ArtifactReader reader(path.string());
        (void)reader.metadata();
    } catch (const std::exception&) {
        return;
    }
    throw std::runtime_error("expected reader error: " + label);
}

void test_round_trip(const std::filesystem::path& root) {
    const auto path = root / "roundtrip.ffaf";
    ArtifactMetadata metadata;
    metadata.codec = kArtifactCodecPngBGRA;
    metadata.width = 160;
    metadata.height = 120;
    metadata.fps_num = 12;
    metadata.fps_den = 1;
    metadata.frame_count = 2;
    metadata.segment_index = 3;
    metadata.duration_ms = 167;

    const std::vector<std::uint8_t> first{1, 2, 3, 4};
    const std::vector<std::uint8_t> second{5, 6, 7};

    ArtifactWriter writer(path.string(), metadata);
    writer.write_frame(0, 83, first);
    writer.write_frame(1, 84, second);
    writer.close();

    ArtifactReader reader(path.string());
    const auto& got = reader.metadata();
    require(got.codec == metadata.codec, "codec mismatch");
    require(got.width == metadata.width, "width mismatch");
    require(got.height == metadata.height, "height mismatch");
    require(got.fps_num == metadata.fps_num, "fps_num mismatch");
    require(got.fps_den == metadata.fps_den, "fps_den mismatch");
    require(got.frame_count == metadata.frame_count, "frame_count mismatch");
    require(got.segment_index == metadata.segment_index, "segment_index mismatch");
    require(got.duration_ms == metadata.duration_ms, "duration_ms mismatch");

    ArtifactFrame frame;
    require(reader.next_frame(frame), "first frame missing");
    require(frame.frame_index == 0, "first frame index mismatch");
    require(frame.duration_ms == 83, "first frame duration mismatch");
    require(frame.payload == first, "first payload mismatch");

    require(reader.next_frame(frame), "second frame missing");
    require(frame.frame_index == 1, "second frame index mismatch");
    require(frame.duration_ms == 84, "second frame duration mismatch");
    require(frame.payload == second, "second payload mismatch");

    require(!reader.next_frame(frame), "unexpected third frame");
}

void test_bad_magic(const std::filesystem::path& root) {
    const auto path = root / "bad_magic.ffaf";
    write_bytes(path, {'N', 'O', 'P', 'E'});
    expect_reader_error(path, "bad magic");
}

void test_bad_version(const std::filesystem::path& root) {
    const auto path = root / "bad_version.ffaf";
    auto bytes = valid_header_with_version(999);
    write_bytes(path, bytes);
    expect_reader_error(path, "bad version");
}

void test_truncated_header(const std::filesystem::path& root) {
    const auto path = root / "truncated_header.ffaf";
    auto bytes = valid_header_with_version(framefleet_engine::kArtifactVersion);
    bytes.resize(20);
    write_bytes(path, bytes);
    expect_reader_error(path, "truncated header");
}

void test_payload_guard(const std::filesystem::path& root) {
    const auto path = root / "payload_guard.ffaf";
    auto bytes = valid_header_with_version(framefleet_engine::kArtifactVersion);
    write_u32_le(bytes, 0);
    write_u32_le(bytes, 1000);
    write_u64_le(bytes, framefleet_engine::kArtifactMaxPayloadBytes + 1);
    write_bytes(path, bytes);

    ArtifactReader reader(path.string());
    ArtifactFrame frame;
    try {
        (void)reader.next_frame(frame);
    } catch (const std::exception&) {
        return;
    }
    throw std::runtime_error("expected payload guard error");
}

}  // namespace

int main() {
    try {
        const auto root = temp_root();
        test_round_trip(root);
        test_bad_magic(root);
        test_bad_version(root);
        test_truncated_header(root);
        test_payload_guard(root);
        std::cout << "artifact tests passed\n";
        return 0;
    } catch (const std::exception& err) {
        std::cerr << "artifact test failed: " << err.what() << "\n";
        return 1;
    }
}
