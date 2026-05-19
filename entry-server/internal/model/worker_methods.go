package model

import "framefleet/pkg/protocol"

func (w *Worker) Supports(taskType protocol.TaskType) bool {
	for _, supported := range w.SupportedTasks {
		if supported == taskType {
			return true
		}
	}
	return false
}
