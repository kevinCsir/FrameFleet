package spool

import (
	"fmt"
	"path/filepath"
)

type Manager struct {
	root string
}

func New(dataDir string) *Manager {
	return &Manager{root: filepath.Join(dataDir, "spool")}
}

func (m *Manager) Root() string {
	return m.root
}

func (m *Manager) Dirs() []string {
	return []string{
		filepath.Join(m.root, "uploads"),
		filepath.Join(m.root, "outgoing"),
		filepath.Join(m.root, "artifacts"),
		filepath.Join(m.root, "results"),
		filepath.Join(m.root, "tmp"),
	}
}

func (m *Manager) UploadPath(taskID string) string {
	return filepath.Join(m.root, "uploads", taskID+".input")
}

func (m *Manager) TempUploadPath(taskID string) string {
	return filepath.Join(m.root, "tmp", taskID+".upload.tmp")
}

func (m *Manager) ArtifactPath(taskID string) string {
	return filepath.Join(m.root, "artifacts", taskID+".segment")
}

func (m *Manager) ResultPath(resultName string) string {
	return filepath.Join(m.root, "results", resultName)
}

func (m *Manager) AssembleArtifactPath(jobID string, taskID string) string {
	return filepath.Join(m.root, "tmp", "assemble", jobID, taskID+".segment")
}

func (m *Manager) OutgoingDir(jobID string) string {
	return filepath.Join(m.root, "outgoing", jobID)
}

func (m *Manager) OutgoingSegmentPath(jobID string, segmentIndex int) string {
	return filepath.Join(m.root, "outgoing", jobID, fmt.Sprintf("segment_%d.mp4", segmentIndex))
}
