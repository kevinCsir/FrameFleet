package engineprotocol

const Version = 1

type Operation string

const (
	OpPing                  Operation = "ping"
	OpProcessInternalSimple Operation = "process_internal_simple"
	OpSplitVideo            Operation = "split_video"
	OpProcessSegment        Operation = "process_segment"
	OpAssembleGIF           Operation = "assemble_gif"
)

type ResponseType string

const (
	ResponseTypeCompleted ResponseType = "completed"
	ResponseTypeFailed    ResponseType = "failed"
)

type DataMode string

const (
	DataModeFile DataMode = "file"
)

type FileRef struct {
	Mode      DataMode `json:"mode"`
	Path      string   `json:"path"`
	Name      string   `json:"name,omitempty"`
	SizeBytes int64    `json:"size_bytes,omitempty"`
}

type Request struct {
	Version      int       `json:"version"`
	RequestID    string    `json:"request_id"`
	Operation    Operation `json:"op"`
	SegmentCount int       `json:"segment_count,omitempty"`
	Input        *FileRef  `json:"input,omitempty"`
	Inputs       []FileRef `json:"inputs,omitempty"`
	Output       *FileRef  `json:"output,omitempty"`
	OutputDir    string    `json:"output_dir,omitempty"`
}

type SegmentFile struct {
	SegmentIndex int    `json:"segment_index"`
	Path         string `json:"path"`
	Name         string `json:"name,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
}

type Response struct {
	Version         int           `json:"version"`
	RequestID       string        `json:"request_id"`
	Type            ResponseType  `json:"type"`
	ResultName      string        `json:"result_name,omitempty"`
	ArtifactName    string        `json:"artifact_name,omitempty"`
	Checksum        string        `json:"checksum,omitempty"`
	FrameCount      int           `json:"frame_count,omitempty"`
	DurationMS      int64         `json:"duration_ms,omitempty"`
	OutputSizeBytes int64         `json:"output_size_bytes,omitempty"`
	Segments        []SegmentFile `json:"segments,omitempty"`
	Reason          string        `json:"reason,omitempty"`
	Retryable       bool          `json:"retryable,omitempty"`
}
