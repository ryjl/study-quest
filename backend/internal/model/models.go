package model

// This package holds every GORM model for the StudyQuest backend. The models
// were originally all in models.go (~1700 lines); they have been split by
// domain for navigability. The split is PURE code movement — no logic changes.
//
//   identity.go  — Setting/StorageSource/User/Session/UserCourseAccess + roles
//   grade.go     — Grade / ContentType / SubjectCategory taxonomy + helpers
//   content.go   — Subject/Tag/Course/CourseGrade/Chapter/Episode/Media/Subtitle
//   progress.go  — UserPoint/PointsLedger/UserProgress/Badge/TierDef/WeeklyTime
//   unlock.go    — CourseUnlockTemplate / UserUnlockOverride / UserUnlockAllowedEpisode
//   release.go   — AppRelease (OTA)
//   reading.go   — ReadingSeries/Book/Article + access + progress
//   watch.go     — WatchEvent
//   ai.go        — AIProvider/AIJob/AISummary/Quiz/Question/Answer/AIRun/...
//                  + AIConfig type/methods + Effective*Hint helpers
//   migrate.go   — AutoMigrate + migrateQuizActiveUniqueIndex
//
// Adding a new model: pick the topical file, add the struct there, and register
// it in migrate.go's AutoMigrate call.
