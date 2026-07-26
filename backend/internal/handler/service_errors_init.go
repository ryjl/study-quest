package handler

import (
	"net/http"

	"studyquest/backend/internal/service"
)

// Service sentinel → HTTP mapping registrations. Kept in a separate file so
// httperr.go stays free of the service import (and the domain knowledge of
// which message goes with which sentinel). Add new domain errors here.
func init() {
	// System-seeded rows (subjects/tags/badges with IsSystem=true) may be
	// edited but never deleted — deleting the canonical core set would orphan
	// badges/rules. Same message for all three entity types.
	registerAppError(service.ErrSystemProtected, 403, "系统默认项不可删除（可在编辑里修改名称/颜色）")

	// FK ON DELETE RESTRICT: a subject still referenced by courses cannot be
	// removed until those courses are migrated/deleted.
	registerAppError(service.ErrSubjectInUse, 409, "该资源仍被引用，无法删除；请先迁移或删除引用项后再试")

	// rule_target has no FK to subjects, so hand-authored badge rules survive a
	// subject delete as silent orphans. Refuse with a 409 and tell the admin how
	// many rules block the delete so they can re-target or remove them first.
	registerAppError(service.ErrSubjectHasBadges, 409, "该科目被徽章规则引用，请先处理相关徽章后再删除")

	// PR3 — embedded-subtitle extraction. PGS/VOBSUB/DVB are bitmap codecs;
	// ffmpeg can't transcode them to text. Surface a 400 (not 500) with an
	// actionable hint so the admin knows to use Whisper transcription instead.
	registerAppError(service.ErrBitmapSubtitleNotSupported, 400, "图形字幕无法提取，请用 Whisper 转录")
	// PR3 — the stream index the admin requested isn't a valid text subtitle
	// stream (negative, doesn't exist, points at audio/video, or stale meta).
	// Defense in depth — the UI filters this too, but a direct API call or a
	// swapped source file could still trigger it.
	registerAppError(service.ErrInvalidStreamIndex, 400, "该字幕流不可用，请重新 probe 后再试")
	// PR3 — extracting an embedded track for a language that already has a
	// subtitle row would silently clobber it (SaveSubtitle upserts on
	// episode+language). Refuse with 409 so the admin can delete the old track
	// or pick a distinct language code.
	registerAppError(service.ErrSubtitleLanguageConflict, 409, "该语言已有字幕，请先删除或换一个语言标签")

	// 作业卷(Homework)——service 在 homeworkRepo 未注入(AI 子系统装配漏了作业 repo,
	// 或测试不传)时返回 ErrHomeworkNotEnabled。503 比 500 准:不是后端崩,而是"这个
	// 能力当前不可用",前端据此隐藏作业入口而不是显示崩页。和 aiService==nil 的早返回
	// 用同样的 503 + 文案,保证两条降级路径的响应一致。
	registerAppError(service.ErrHomeworkNotEnabled, http.StatusServiceUnavailable, "作业功能未启用")
}
