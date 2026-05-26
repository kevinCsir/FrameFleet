# FrameFleet C++ Engine

The C++ engine is a local worker subprocess managed by WorkerGo. It does not
expose network endpoints. WorkerGo sends one JSON request per line on stdin, and
the engine writes one JSON response per line on stdout. Logs go to stderr.

C++ intentionally does not know FrameFleet job/task IDs. It only sees
operations and file paths.

## Dependencies

- CMake 3.16+
- C++17 compiler
- `ffmpeg`
- `ffprobe`
- OpenCV 4 development package
- vendored `nlohmann/json` at `third_party/nlohmann/json.hpp`

Ubuntu/Debian:

```bash
sudo apt install cmake g++ ffmpeg libopencv-dev
```

## Build

From the repository root:

```bash
cmake -S worker-node/cpp -B worker-node/cpp/build
cmake --build worker-node/cpp/build
```

Binary:

```text
worker-node/cpp/build/framefleet-engine
```

## Operations

- `ping`: health check.
- `process_internal_simple`: simple copy path retained for protocol/testing.
- `split_video`: uses `ffprobe` to read duration and `ffmpeg` to split mp4.
- `process_segment`: reads mp4 with OpenCV, runs Canny, writes a segment GIF
  artifact.
- `assemble_gif`: reads segment GIF artifacts and either concatenates local
  palettes or re-encodes with a global palette according to `assemble_mode`.

## Threading

The engine is designed around one slot doing one request at a time. Keep work
effectively single-threaded:

- OpenCV calls `cv::setNumThreads(1)`.
- ffmpeg commands use `-threads 1`, `-filter_threads 1`, and
  `-filter_complex_threads 1`.
- Child process environment limits common thread pools such as OpenMP and BLAS.

## Configuration

WorkerGo passes Entry registration processing policy into each slot process:

```text
FRAMEFLEET_CANNY_LOW_THRESHOLD
FRAMEFLEET_CANNY_HIGH_THRESHOLD
```

These values come from Entry env/config:

```text
PROCESS_CANNY_LOW_THRESHOLD=180
PROCESS_CANNY_HIGH_THRESHOLD=360
```

Optional binary overrides:

```text
FRAMEFLEET_FFMPEG_PATH
FRAMEFLEET_FFPROBE_PATH
```

Fake compatibility switches used by older integration tests:

```text
FRAMEFLEET_ENGINE_FAKE_SPLIT=1
FRAMEFLEET_ENGINE_FAKE_PROCESS=1
FRAMEFLEET_ENGINE_FAKE_ASSEMBLE=1
```

## Tests

Build and run C++ tests:

```bash
cmake --build worker-node/cpp/build
ctest --test-dir worker-node/cpp/build --output-on-failure
```

Run the WorkerGo/enginepool real video integration test:

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod \
go test ./worker-node/go/internal/enginepool -run TestCppEngineRealVideoPipeline -count=1
```

The fixed short test video lives at:

```text
worker-node/cpp/testdata/videos/canny_source_short.mp4
```
