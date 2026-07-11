package service

// ──────────────────────────────────────────────────────────────────────────────
// Single source of truth for system-seeded entity keys.
//
// These lists are referenced by BOTH the SeedDefault* methods (which insert
// the rows) AND cmd/server/main.go's markSystemDefaults (which backfills
// is_system=true on pre-existing installs). Keeping them in one place removes
// the old drift risk where main.go hand-redeclared the same keys with a
// comment saying "must stay in sync" — they now literally can't diverge.
// ──────────────────────────────────────────────────────────────────────────────

// SystemSubjectKeys are the keys of the seeded-default subjects. These rows
// carry IsSystem=true and are delete-protected (see ErrSystemProtected).
var SystemSubjectKeys = []string{
	"chinese", "math", "english", "physics", "extra",
}

// SystemTagKeys are the keys of the seeded-default tags.
var SystemTagKeys = []string{
	"required", "thinking", "extension", "explore", "extracurricular", "logic", "horizon",
}

// SystemBadgeCodes are the codes of the seeded-default badges.
var SystemBadgeCodes = []string{
	"first_blood", "seven_days_pioneer", "math_expert", "english_star",
	"hard_worker", "explorer",
}
