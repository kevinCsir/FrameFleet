package model

func (w *Worker) Supports(taskType TaskType) bool {
	for _, supported := range w.SupportedTasks {
		if supported == taskType {
			return true
		}
	}
	return false
}
