cmake_minimum_required(VERSION 3.16)

project(framefleet_engine LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

enable_testing()

find_package(OpenCV REQUIRED COMPONENTS core imgproc imgcodecs videoio)

add_executable(framefleet-engine
    src/main.cpp
    src/protocol.cpp
    src/engine.cpp
    src/artifact.cpp
)

target_include_directories(framefleet-engine PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    ${CMAKE_CURRENT_SOURCE_DIR}/third_party
    ${OpenCV_INCLUDE_DIRS}
)

target_link_libraries(framefleet-engine PRIVATE
    ${OpenCV_LIBS}
)

add_executable(framefleet-artifact-test
    tests/artifact_test.cpp
    src/artifact.cpp
)

target_include_directories(framefleet-artifact-test PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)

add_test(NAME framefleet-artifact-test COMMAND framefleet-artifact-test)
