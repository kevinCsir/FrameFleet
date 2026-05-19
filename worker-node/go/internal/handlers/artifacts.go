package handlers

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetArtifact(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "failed", "reason": "missing task_id"})
		return
	}

	path := h.spool.ArtifactPath(taskID)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"status": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "failed", "reason": err.Error()})
		return
	}

	c.File(path)
}
