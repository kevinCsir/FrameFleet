package handlers

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetResult(c *gin.Context) {
	resultName := c.Param("result_name")
	if resultName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "failed", "reason": "missing result_name"})
		return
	}

	path := h.spool.ResultPath(resultName)
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
