#include "framefleet_engine/engine.hpp"
#include "framefleet_engine/protocol.hpp"

#include <exception>
#include <iostream>
#include <string>

int main() {
    std::cerr << "framefleet engine started" << std::endl;

    std::string line;
    while (std::getline(std::cin, line)) {
        if (line.empty()) {
            continue;
        }

        try {
            const auto request = framefleet_engine::parse_request_line(line);
            const auto response = framefleet_engine::handle_request(request);
            std::cout << framefleet_engine::response_to_json(response).dump() << std::endl;
        } catch (const std::exception& err) {
            const auto response = framefleet_engine::make_failed_response("", "", "", err.what(), false);
            std::cout << framefleet_engine::response_to_json(response).dump() << std::endl;
        }
    }

    return 0;
}
