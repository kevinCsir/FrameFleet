package protocol

type SegmentUploadStatus string

const (
	SegmentUploadStatusSuccess SegmentUploadStatus = "success"
	SegmentUploadStatusFailed  SegmentUploadStatus = "failed"
)

type SegmentUploadResponse struct {
	Status SegmentUploadStatus `json:"status"`
	Reason string              `json:"reason,omitempty"`
}
