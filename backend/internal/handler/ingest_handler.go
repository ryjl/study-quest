package handler

import (
	"net/http"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// IngestHandler manages bulk ingest pipelines from local tool scripts.
type IngestHandler interface {
	IngestEpisodes(c *gin.Context)
	IngestAIContent(c *gin.Context)
}

type ingestHandler struct {
	episodeRepo    repository.EpisodeRepository
	episodeService service.EpisodeService
}

// NewIngestHandler creates an instance of IngestHandler.
func NewIngestHandler(er repository.EpisodeRepository, es service.EpisodeService) IngestHandler {
	return &ingestHandler{
		episodeRepo:    er,
		episodeService: es,
	}
}

type episodeIngestRequest struct {
	CourseID             uint   `json:"course_id" binding:"required"`
	Title                string `json:"title" binding:"required"`
	VideoRelativePath    string `json:"video_relative_path" binding:"required"`
	FileHash             string `json:"file_hash"`
	OriginalRelativePath string `json:"original_relative_path"`
	FileSize             *int64 `json:"file_size"`
	DurationSeconds      *int   `json:"duration_seconds"`
	SortOrder            int    `json:"sort_order"`
	AttachmentJSON       string `json:"attachment_json"`
}

func (h *ingestHandler) IngestEpisodes(c *gin.Context) {
	var req []episodeIngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid format, expected array of episodes"})
		return
	}

	importedCount := 0
	updatedCount := 0

	for _, reqEp := range req {
		// Look up existing episode using double protection index (hash first, then path+size)
		var existing *model.Episode
		var err error

		if reqEp.FileHash != "" {
			existing, err = h.episodeRepo.FindByHash(reqEp.FileHash)
		}

		if (err != nil || existing == nil) && reqEp.VideoRelativePath != "" && reqEp.FileSize != nil {
			existing, err = h.episodeRepo.FindByPathAndSize(reqEp.VideoRelativePath, *reqEp.FileSize)
		}

		if existing != nil {
			// Update metadata
			existing.Title = reqEp.Title
			existing.VideoRelativePath = reqEp.VideoRelativePath
			if reqEp.OriginalRelativePath != "" {
				existing.OriginalRelativePath = reqEp.OriginalRelativePath
			}
			if reqEp.FileSize != nil {
				existing.FileSize = reqEp.FileSize
			}
			if reqEp.DurationSeconds != nil {
				existing.DurationSeconds = reqEp.DurationSeconds
			}
			if reqEp.SortOrder > 0 {
				existing.SortOrder = reqEp.SortOrder
			}
			if reqEp.AttachmentJSON != "" {
				existing.AttachmentJSON = reqEp.AttachmentJSON
			}

			if err := h.episodeRepo.Update(existing); err == nil {
				updatedCount++
			}
		} else {
			// Create new
			attachments := reqEp.AttachmentJSON
			if attachments == "" {
				attachments = "[]"
			}
			origPath := reqEp.OriginalRelativePath
			if origPath == "" {
				origPath = reqEp.VideoRelativePath
			}

			_, err = h.episodeService.CreateEpisode(
				reqEp.CourseID,
				0, // Default ChapterID
				reqEp.Title,
				reqEp.VideoRelativePath,
				attachments,
				reqEp.SortOrder,
				reqEp.FileHash,
				origPath,
				reqEp.FileSize,
				reqEp.DurationSeconds,
			)
			if err == nil {
				importedCount++
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"imported": importedCount,
		"updated":  updatedCount,
		"status":   "success",
	})
}

func (h *ingestHandler) IngestAIContent(c *gin.Context) {
	var req struct {
		EpisodeID        uint   `json:"episode_id" binding:"required"`
		PreAdventureJSON string `json:"pre_adventure_json"`
		PostReviewJSON    string `json:"post_review_json"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body format"})
		return
	}

	// Verify episode exists
	ep, err := h.episodeRepo.FindByID(req.EpisodeID)
	if err != nil || ep == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "associated episode not found"})
		return
	}

	err = h.episodeService.SaveAIContent(req.EpisodeID, req.PreAdventureJSON, req.PostReviewJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save AI content: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "episode_id": req.EpisodeID})
}
