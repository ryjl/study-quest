package handler

// admin_content.go was the original 751-line file holding admin endpoints for
// courses, episodes, chapters, and subtitles. Split for navigability:
//   admin_course.go    — course CRUD + parseGrades
//   admin_episode.go   — episode CRUD + bulk + list
//   admin_chapter.go   — chapter CRUD + reorder
//   admin_subtitle.go  — subtitle CRUD + extract + automatch
