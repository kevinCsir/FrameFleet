# FFAF v1

FFAF is the FrameFleet Artifact Format used by the C++ engine for phase-one
segment artifacts. Go treats artifacts as opaque single files; only C++ writes
and reads this format.

## Encoding

All integer fields are unsigned little-endian. Implementations must write fields
explicitly and must not dump C/C++ structs, because struct padding is not part of
the format.

## File Layout

```text
file = header frame_record[frame_count]
```

The header is exactly 64 bytes:

```text
offset  size  field
0       4     magic = "FFAF"
4       2     version = 1
6       2     header_size = 64
8       4     flags
12      4     codec
16      4     width
20      4     height
24      4     fps_num
28      4     fps_den
32      4     frame_count
36      4     segment_index
40      8     duration_ms
48      8     reserved = 0
56      8     reserved = 0
```

Frame records are stored sequentially:

```text
offset  size  field
0       4     frame_index
4       4     duration_ms
8       8     payload_size
16      N     payload bytes
```

## Codecs

```text
1 = PNG_BGRA
```

`PNG_BGRA` means the payload is one PNG image with an alpha channel. The decoded
frame dimensions must match `width` and `height`.

## Validation

Readers must reject artifacts when:

- `magic` is not `FFAF`.
- `version` is unsupported.
- `header_size` is smaller than 64.
- `codec` is unsupported.
- `width`, `height`, `fps_num`, `fps_den`, or `frame_count` is zero.
- A frame payload is truncated.
- A frame payload declares a size above the implementation guard limit.

## Compatibility

Writers currently emit version 1. Readers should dispatch by `version` when a
future version is added. New payload encodings must use new `codec` values
instead of changing the meaning of existing codec values.
