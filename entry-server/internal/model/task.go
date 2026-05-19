package model

type TaskType string

const (
	TaskTypeProcessSegment TaskType = "process_segment"
	TaskTypeAssembleGIF    TaskType = "assemble_gif"
)

func IsSupportedTaskType(taskType TaskType) bool {
	switch taskType {
	case TaskTypeProcessSegment, TaskTypeAssembleGIF:
		return true
	default:
		return false
	}
}
