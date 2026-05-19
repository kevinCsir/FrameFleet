# FrameFleet C++ Engine

The C++ engine is a local worker subprocess managed by the Go worker agent. It
does not expose network endpoints. Go sends one JSON request per line on stdin,
and the engine writes one JSON response per line on stdout. Logs must go to
stderr.

## Dependencies

The engine currently uses:

- CMake 3.16+
- A C++17 compiler
- vendored `nlohmann/json` single header at `third_party/nlohmann/json.hpp`

On Ubuntu/Debian:

```bash
sudo apt install cmake g++
```

## Build

From the repository root:

```bash
cmake -S worker-node/cpp -B worker-node/cpp/build
cmake --build worker-node/cpp/build
```

The binary is expected at:

```text
worker-node/cpp/build/framefleet-engine
```

## Current Behavior

This is a fake engine scaffold. It parses the Go/C++ JSON Lines protocol and
simulates video work with file operations:

- `ping`: returns `completed`
- `process_internal_simple`: copies input file to output file
- `split_video`: copies input file into one output file per requested segment
- `process_segment`: copies segment input file to artifact output file
- `assemble_gif`: concatenates artifact inputs into the result output file

Real video parsing, processing, and GIF generation will replace these fake
handlers later without changing the process protocol.
