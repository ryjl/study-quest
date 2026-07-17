package service

import "errors"

// ErrSystemProtected is returned when attempting to delete a seeded-default
// row (Subject / Tag / Badge marked IsSystem=true). Such rows may be renamed
// or recolored but never deleted, so the catalog always retains its canonical
// core set. The handler layer translates this into a 403 with a friendly
// Chinese message.
var ErrSystemProtected = errors.New("system-protected item cannot be deleted")

// ErrSubjectInUse is returned when deleting a subject that still has courses
// referencing it. The DB-level FK (ON DELETE RESTRICT) is the real guard; this
// sentinel just lets the handler translate it into a clean 409 response.
var ErrSubjectInUse = errors.New("subject is still referenced by courses or badges")

// ErrSubjectHasBadges is returned when deleting a subject that is still the
// rule_target of one or more HAND-AUTHORED badges. rule_target has no FK to
// subjects, so there is no DB-level guard — this service-level count is the
// only defense. Deleting the subject anyway would silently leave those badge
// rules permanently un-matchable, so the delete is refused (the admin must
// re-target or delete the badges first). The subject's own auto-generated
// subject_<key> badge does NOT trigger this: the delete path removes it.
var ErrSubjectHasBadges = errors.New("subject is still referenced by hand-authored badge rules")
