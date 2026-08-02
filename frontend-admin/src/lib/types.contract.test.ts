import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { Course } from './types';

// types.contract.test.ts — cross-layer contract guard (P3, TS side).
//
// The Go admin DTOs, these TS interfaces, and the Dart model classes are a
// HAND-MAINTAINED 3-way contract (no codegen). The Go test
// (backend/cmd/server/dto_contract_test.go) writes GOLDEN snapshots of the
// real /admin/api/courses response into backend/cmd/server/testdata/. This
// test loads those snapshots and asserts the TS `Course` interface can
// deserialize them and that the fields the admin UI actually reads are present.
//
// The trap: when a Go DTO field is renamed/removed, the committed golden
// snapshot changes (reviewable diff) AND this test breaks if the TS interface
// wasn't updated to match — turning a silent cross-layer drift into a loud,
// local failure.
//
// If you intentionally change a Go DTO field: regenerate the golden files
// (delete backend/cmd/server/testdata/*.json, re-run the Go test), then update
// the TS interface here to follow.

// goldenDir resolves to backend/cmd/server/testdata relative to this file.
// frontend-admin/src/lib/ → up to repo root (../../..) then into backend.
const goldenDir = resolve(__dirname, '../../../backend/cmd/server/testdata');

function loadGoldenArray(name: string): any[] {
  const raw = readFileSync(resolve(goldenDir, name), 'utf8');
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed)) {
    throw new Error(`golden ${name}: expected a JSON array, got ${typeof parsed}`);
  }
  return parsed as any[];
}

describe('cross-layer DTO contract — courses', () => {
  // The fields the admin Courses UI reads off each row. If the Go DTO renames
  // any of these, the snapshot changes and the assertion below fails — forcing
  // the TS interface + UI call sites to follow. Add fields here as the UI grows
  // to read more of the row.
  const requiredKeys: (keyof Course)[] = [
    'id',
    'title',
    'subject',
    'cover_url',
    'attachment_json',
  ];

  it('courses-admin.json deserializes into Course[] with required keys', () => {
    const rows = loadGoldenArray('courses-admin.json');
    expect(rows.length).toBeGreaterThan(0);
    for (const row of rows as Course[]) {
      for (const key of requiredKeys) {
        expect(row).toHaveProperty(key);
      }
      // id is a number (the UI keys React lists off it).
      expect(typeof row.id).toBe('number');
    }
  });

  it('courses-admin.json seeded course 契约快照 is present (sanity)', () => {
    // If this fails the golden file wasn't regenerated after a schema/seed
    // change — re-run the Go TestContract_WriteCoursesGolden test.
    const rows = loadGoldenArray('courses-admin.json');
    expect(rows.some((r) => r.title === '契约快照')).toBe(true);
  });

  it('courses-client.json deserializes (PascalCase client shape is valid JSON)', () => {
    // The client endpoint emits PascalCase (different from admin's snake_case).
    // We don't share the TS interface with the client (that's Dart's job), but
    // we assert the file is parseable + non-empty so the Go-side snapshot
    // generation didn't silently regress to an error body.
    const rows = loadGoldenArray('courses-client.json');
    expect(rows.length).toBeGreaterThan(0);
    // The client shape's ID field is PascalCase "ID" (GORM default struct
    // marshaling for the Flutter-facing handlers). Assert it's present so a Go
    // change that flipped the client to snake_case would break here.
    expect(rows[0]).toHaveProperty('ID');
  });
});
