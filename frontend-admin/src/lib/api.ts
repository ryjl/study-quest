// Centralized API client — aggregator.
//
// The actual endpoint definitions live in domain files under ./api/<domain>.ts
// (auth, courses, episodes, ... ai). Each domain file exports a sub-object
// that holds its methods; this module spreads them all into a single flat
// `api` object so every existing `api.foo()` call site keeps working
// unchanged. The HTTP plumbing (request, qs, ApiError) lives in ./api/_request.
//
// Same-origin cookies carry the admin session. All endpoints return typed
// data; errors throw an ApiError with the server message.

// Re-export so existing `import { ApiError } from './api'` (Login.tsx, tests)
// keeps resolving without callers knowing about the new folder layout.
export { ApiError } from './api/_request';

import { auth } from './api/auth';
import { dashboard } from './api/dashboard';
import { courses } from './api/courses';
import { episodes } from './api/episodes';
import { chapters } from './api/chapters';
import { users } from './api/users';
import { badges } from './api/badges';
import { subjects } from './api/subjects';
import { tags } from './api/tags';
import { grades } from './api/grades';
import { subtitles } from './api/subtitles';
import { imports } from './api/imports';
import { storage } from './api/storage';
import { settings } from './api/settings';
import { probe } from './api/probe';
import { subtitleJobs } from './api/subtitleJobs';
import { unlock } from './api/unlock';
import { uploads } from './api/uploads';
import { releases } from './api/releases';
import { reading } from './api/reading';
import { ai } from './api/ai';

// Flat aggregator. Method names are unique across domains (verified before the
// split), so the spread is safe — no later domain silently shadows an earlier
// one.
export const api = {
  ...auth,
  ...dashboard,
  ...courses,
  ...episodes,
  ...chapters,
  ...users,
  ...badges,
  ...subjects,
  ...tags,
  ...grades,
  ...subtitles,
  ...imports,
  ...storage,
  ...settings,
  ...probe,
  ...subtitleJobs,
  ...unlock,
  ...uploads,
  ...releases,
  ...reading,
  ...ai,
};
