package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/service"
)


func (h *adminHandler) Scan(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}

	files, err := h.importService.ScanPath(path)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, files)
}

func (h *adminHandler) PreviewTree(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		path = "/"
	}

	tree, err := h.importService.PreviewDeepScan(path)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, tree)
}

func (h *adminHandler) ExecuteImport(c *gin.Context) {
	var req service.ExecuteTreeImportRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format: " + err.Error()})
		return
	}

	err := h.importService.ExecuteTreeImport(&req)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
	})
}

func (h *adminHandler) ScanAttachments(c *gin.Context) {
	entityType := c.Query("type") // "course", "chapter", "episode"
	idStr := c.Query("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var targetPath string
	switch entityType {
	case "episode":
		ep, err := h.episodeRepo.FindByID(uint(id))
		if err != nil || ep == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Episode not found"})
			return
		}
		if ep.OriginalRelativePath != "" {
			targetPath = filepath.ToSlash(filepath.Dir(ep.OriginalRelativePath))
		}
	case "chapter":
		ch, err := h.chapterService.GetChapterByID(uint(id))
		if err != nil || ch == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found"})
			return
		}
		eps, err := h.episodeRepo.ListByCourse(ch.CourseID)
		if err == nil {
			for _, ep := range eps {
				if ep.ChapterID == ch.ID && ep.OriginalRelativePath != "" {
					targetPath = filepath.ToSlash(filepath.Dir(ep.OriginalRelativePath))
					break
				}
			}
		}
	case "course":
		cr, err := h.courseRepo.FindByID(uint(id))
		if err != nil || cr == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Course not found"})
			return
		}
		eps, err := h.episodeRepo.ListByCourse(cr.ID)
		if err == nil && len(eps) > 0 {
			for _, ep := range eps {
				if ep.OriginalRelativePath != "" {
					targetPath = filepath.ToSlash(filepath.Dir(ep.OriginalRelativePath))
					break
				}
			}
		}
	}

	if targetPath == "" || targetPath == "." {
		targetPath = "/"
	}

	files, err := h.importService.ScanDirectoryAttachments(targetPath)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"path":  targetPath,
		"files": files,
	})
}

func (h *adminHandler) UploadImage(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file uploaded"})
		return
	}

	uploadDir := "./data/uploads"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload dir"})
		return
	}

	ext := filepath.Ext(file.Filename)
	randomName := fmt.Sprintf("%d_%s%s", time.Now().UnixNano(), generateRandomString(4), ext)
	dst := filepath.Join(uploadDir, randomName)

	if err := c.SaveUploadedFile(file, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	urlPath := "/uploads/" + randomName
	c.JSON(http.StatusOK, gin.H{"url": urlPath})
}

func (h *adminHandler) ScanMissingDurations(c *gin.Context) {
	if h.probeWorker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "failed",
			"error":  "probe worker is not initialized",
		})
		return
	}
	episodes, err := h.episodeRepo.ListByNullDuration()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "failed",
			"error":  "failed to query episodes: " + err.Error(),
		})
		return
	}

	ids := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	enqueued := h.probeWorker.EnqueueBatch(ids)
	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"queued":   enqueued,
		"total":    len(ids),
		"message":  fmt.Sprintf("已排队 %d 集等待探测时长（串行限速，约每集 4 秒）", enqueued),
	})
}

// ProbeProgress returns the worker's current progress snapshot. The admin UI
// polls this every couple of seconds while a scan is running.
func (h *adminHandler) ProbeProgress(c *gin.Context) {
	if h.probeWorker == nil {
		c.JSON(http.StatusOK, gin.H{"running": false})
		return
	}
	c.JSON(http.StatusOK, h.probeWorker.Stats())
}
