cmake_minimum_required(VERSION 3.16)

project(framefleet_engine LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)


add_executable(framefleet-engine
    src/main.cpp
    src/protocol.cpp
    src/engine.cpp
)

target_include_directories(framefleet-engine PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    ${CMAKE_CURRENT_SOURCE_DIR}/third_party
)
