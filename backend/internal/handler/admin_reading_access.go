package handler

import (
	"errors"
	"fmt"
	"net/http"
	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/model"
)

// Code split from admin_reading.go for navigability.
// Reading access grant/revoke/bulk + helpers.

// errInvalidTargetType is returned by readingGrant/readingRevoke when the
// target_type is not one of series / book / article.
var errInvalidTargetType = errors.New("invalid target_type: must be series, book, or article")

func (h *adminHandler) GrantReadingAccess(c *gin.Context) {
	var req struct {
		UserID     uint   `json:"user_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
		TargetID   uint   `json:"target_id" binding:"required"`
	}
	if !bindJSON(c, &req) { return }
	if err := h.readingGrant(req.UserID, req.TargetType, req.TargetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "granted"})
}

func (h *adminHandler) RevokeReadingAccess(c *gin.Context) {
	var req struct {
		UserID     uint   `json:"user_id" binding:"required"`
		TargetType string `json:"target_type" binding:"required"`
		TargetID   uint   `json:"target_id" binding:"required"`
	}
	if !bindJSON(c, &req) { return }
	if err := h.readingRevoke(req.UserID, req.TargetType, req.TargetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

// BulkReadingAccess grants or revokes ALL reading resources of all three types
// for a user. action = "grant_all" | "revoke_all".
func (h *adminHandler) BulkReadingAccess(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	var req struct {
		Action string `json:"action" binding:"required"`
	}
	if !bindJSON(c, &req) { return }
	switch req.Action {
	case "grant_all":
		// Articles have no storage dimension (web URLs) — always grant in bulk.
		if err := h.readingArticleRepo.GrantAll(id); err != nil {
			respondError(c, err)
			return
		}
		// Series + books DO have a storage dimension (series access implies
		// child-book access via StreamBook's CanAccess inheritance). The allow-
		// list is default-deny: a user with an EMPTY list is allowed nothing,
		// so grant_all refuses with a hint. With a non-empty list we grant per
		// item, skipping any series/book whose source isn't allowed.
		if h.storageSourceRepo != nil {
			wl, werr := h.storageSourceRepo.WhitelistForUser(id)
			if werr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read storage whitelist"})
				return
			}
			if len(wl) == 0 {
				c.JSON(http.StatusForbidden, gin.H{"error": "该用户未被允许任何存储源，请先在用户详情里勾选至少一个存储源"})
				return
			}
			// Load every book ONCE and group source ids by series in memory.
			allBooks, berr := h.readingBookRepo.List("", 0, nil, false)
			if berr != nil {
				respondError(c, berr)
				return
			}
			seriesSources := make(map[uint][]uint)
			for _, b := range allBooks {
				if b.SeriesID != nil && b.SourceID != nil {
					seriesSources[*b.SeriesID] = append(seriesSources[*b.SeriesID], *b.SourceID)
				}
			}
			seriesGranted, seriesSkipped := 0, 0
			allSeries, serr := h.readingSeriesRepo.List("", 0, nil)
			if serr != nil {
				respondError(c, serr)
				return
			}
			for _, s := range allSeries {
				// A series grant implies access to ALL its books (via
				// StreamBook's CanAccess series inheritance), so the series is
				// allowed only if EVERY one of its books' sources is in the
				// allow-list.
				if werr := checkStorageWhitelist(h.storageSourceRepo, id, seriesSources[s.ID]); werr != nil {
					seriesSkipped++
					continue
				}
				if err := h.readingSeriesRepo.GrantAccess(id, s.ID); err != nil {
					respondError(c, err)
					return
				}
				seriesGranted++
			}
			booksGranted, booksSkipped := 0, 0
			for _, b := range allBooks {
				var ids []uint
				if b.SourceID != nil {
					ids = []uint{*b.SourceID}
				}
				if werr := checkStorageWhitelist(h.storageSourceRepo, id, ids); werr != nil {
					booksSkipped++
					continue
				}
				if err := h.readingBookRepo.GrantAccess(id, b.ID); err != nil {
					respondError(c, err)
					return
				}
				booksGranted++
			}
			c.JSON(http.StatusOK, gin.H{
				"status":  "success",
				"message": fmt.Sprintf("已授权 %d 套/%d 本，跳过 %d 套/%d 本（存储源白名单）", seriesGranted, booksGranted, seriesSkipped, booksSkipped),
			})
			return
		}
		// Feature unwired (nil repo) → unrestricted bulk grant.
		if err := h.readingSeriesRepo.GrantAll(id); err != nil {
			respondError(c, err)
			return
		}
		if err := h.readingBookRepo.GrantAll(id); err != nil {
			respondError(c, err)
			return
		}
	case "revoke_all":
		if err := h.readingSeriesRepo.RevokeAll(id); err != nil {
			respondError(c, err)
			return
		}
		if err := h.readingBookRepo.RevokeAll(id); err != nil {
			respondError(c, err)
			return
		}
		if err := h.readingArticleRepo.RevokeAll(id); err != nil {
			respondError(c, err)
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action: " + req.Action})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// readingSourceIDsForGrant expands a reading grant target into the set of
// storage source ids it would expose. Articles have no source dimension (nil);
// books contribute their own SourceID; a series contributes the union of its
// books' source ids (series access implies child access).
func (h *adminHandler) readingSourceIDsForGrant(targetType string, targetID uint) ([]uint, error) {
	switch targetType {
	case "series":
		books, err := h.readingBookRepo.ListBySeries(targetID)
		if err != nil {
			return nil, err
		}
		ids := make([]uint, 0, len(books))
		for _, b := range books {
			if b.SourceID != nil {
				ids = append(ids, *b.SourceID)
			}
		}
		return ids, nil
	case "book":
		book, err := h.readingBookRepo.FindByID(targetID)
		if err != nil {
			return nil, err
		}
		if book == nil {
			// A miss should NOT be treated as "allowed" — surface it so the
			// caller refuses instead of silently granting access to nothing.
			return nil, fmt.Errorf("reading book %d not found", targetID)
		}
		if book.SourceID == nil {
			return nil, nil
		}
		return []uint{*book.SourceID}, nil
	case "article":
		return nil, nil
	}
	return nil, nil
}

func (h *adminHandler) readingGrant(userID uint, targetType string, targetID uint) error {
	// Storage-source whitelist gate (防呆). Staff roles bypass — they manage
	// content, not consume under the gate.
	if h.storageSourceRepo != nil {
		if u, uerr := h.userRepo.FindByID(userID); uerr == nil && u != nil && !model.IsStaffRole(u.Role) {
			sourceIDs, serr := h.readingSourceIDsForGrant(targetType, targetID)
			if serr != nil {
				return serr
			}
			if werr := checkStorageWhitelist(h.storageSourceRepo, userID, sourceIDs); werr != nil {
				return werr
			}
		}
	}
	switch targetType {
	case "series":
		return h.readingSeriesRepo.GrantAccess(userID, targetID)
	case "book":
		return h.readingBookRepo.GrantAccess(userID, targetID)
	case "article":
		return h.readingArticleRepo.GrantAccess(userID, targetID)
	}
	return errInvalidTargetType
}

func (h *adminHandler) readingRevoke(userID uint, targetType string, targetID uint) error {
	switch targetType {
	case "series":
		return h.readingSeriesRepo.RevokeAccess(userID, targetID)
	case "book":
		return h.readingBookRepo.RevokeAccess(userID, targetID)
	case "article":
		return h.readingArticleRepo.RevokeAccess(userID, targetID)
	}
	return errInvalidTargetType
}

// ── Folder Import ──

// PreviewReadingImport scans a storage folder and returns a preview tree.
// GET /admin/api/reading-import/preview-tree?path=
