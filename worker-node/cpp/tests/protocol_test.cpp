#include "framefleet_engine/protocol.hpp"

#include <iostream>
#include <stdexcept>
#include <string>

namespace {

using framefleet_engine::kGIFAssembleModeGlobalPaletteRecode;
using framefleet_engine::kGIFAssembleModeLocalPaletteConcat;
using framefleet_engine::parse_request_line;

void require(bool condition, const std::string& message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

std::string assemble_request(const std::string& assemble_mode_json) {
    return std::string(R"({
        "version": 1,
        "request_id": "req_assemble_1",
        "op": "assemble_gif",)") +
           assemble_mode_json + R"(
        "inputs": [
            {
                "mode": "file",
                "path": "/tmp/task_001.segment",
                "name": "task_001.segment"
            }
        ],
        "output": {
            "mode": "file",
            "path": "/tmp/job_123.gif",
            "name": "job_123.gif"
        }
    })";
}

void test_default_assemble_mode() {
    const auto request = parse_request_line(assemble_request(""));
    require(request.assemble_mode == kGIFAssembleModeLocalPaletteConcat,
            "assemble mode should default to local palette concat");
}

void test_valid_assemble_modes() {
    const auto local = parse_request_line(
        assemble_request(R"("assemble_mode": "local_palette_concat",)"));
    require(local.assemble_mode == kGIFAssembleModeLocalPaletteConcat,
            "local palette concat mode mismatch");

    const auto global = parse_request_line(
        assemble_request(R"("assemble_mode": "global_palette_recode",)"));
    require(global.assemble_mode == kGIFAssembleModeGlobalPaletteRecode,
            "global palette recode mode mismatch");
}

void test_invalid_assemble_mode() {
    try {
        (void)parse_request_line(assemble_request(R"("assemble_mode": "bad_mode",)"));
    } catch (const std::exception&) {
        return;
    }
    throw std::runtime_error("expected invalid assemble mode to fail");
}

}  // namespace

int main() {
    try {
        test_default_assemble_mode();
        test_valid_assemble_modes();
        test_invalid_assemble_mode();
        std::cout << "protocol tests passed\n";
        return 0;
    } catch (const std::exception& err) {
        std::cerr << "protocol test failed: " << err.what() << "\n";
        return 1;
    }
}
