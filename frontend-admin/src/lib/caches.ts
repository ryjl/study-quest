// Runtime caches for the subject + tag catalogs. The admin SPA used to ship a
// hardcoded SUBJECTS constant in types.ts; subjects are now DB-driven and
// editable. useSubjects() / useTags() (in lib/use*.ts) fetch the catalogs and
// seed these caches so that the plain subjectMeta() / tagMetaByID() helpers —
// used outside React render paths or in legacy call sites — keep resolving
// without a hook.
//
// Split out of types.ts so that file is pure TS interfaces (no runtime state).

import type { SubjectMeta, TagMeta } from './types';

let subjectCache: SubjectMeta[] = [];

export function setSubjectCache(list: SubjectMeta[]) {
  subjectCache = list;
}

export function subjectMeta(key: string): SubjectMeta {
  return (
    subjectCache.find((s) => s.key === key) ?? {
      key,
      label: key,
      color: '#9ca3af',
    }
  );
}

let tagCache: TagMeta[] = [];

export function setTagCache(list: TagMeta[]) {
  tagCache = list;
}

export function tagMetaByID(id: number): TagMeta | undefined {
  return tagCache.find((t) => t.id === id);
}
