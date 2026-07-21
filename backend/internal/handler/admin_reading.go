package handler

// admin_reading.go was the original 739-line file holding admin endpoints for
// the reading room (series / books / articles). Split for navigability:
//   admin_reading_series.go  — series CRUD
//   admin_reading_book.go    — book CRUD
//   admin_reading_article.go — article CRUD
//   admin_reading_access.go  — access grant/revoke/bulk + helpers
//   admin_reading_import.go  — import preview/execute + whitelist suggest
