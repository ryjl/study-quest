package handler

// admin_ai.go was the original 1480-line file holding every admin-side AI
// endpoint. It has been split for navigability into:
//
//   admin_ai_provider.go  — AI provider CRUD + test/diagnose helpers
//   admin_ai_jobs.go      — async job enqueue / list / reset / retry / skip
//   admin_ai_results.go   — summary / quiz / course-summary / user-report / preview
//   admin_ai_lifecycle.go — regenerate + delete endpoints
//
// This file no longer holds any code itself; it exists only as the index.
