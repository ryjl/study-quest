package handler

import "studyquest/backend/internal/service"

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
}
