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
//   wrong_book.go — WrongBookItem (错题本 curation 状态)
//   exam.go      — Exam/ExamQuestion/ExamAnswer (课程考试,平行于 Quiz/Question/Answer)
//   migrate.go   — AutoMigrate + migrateQuizActiveUniqueIndex + migrateExamActiveUniqueIndex
//
// Adding a new model: pick the topical file, add the struct there, and register
// it in migrate.go's AutoMigrate call.
