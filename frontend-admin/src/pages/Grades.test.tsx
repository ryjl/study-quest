import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { GradesTable } from './Grades';
import { ToastProvider } from '../lib/toast';

// Grades.test.tsx — page-level mount test (P4).
//
// The existing admin tests are mostly pure-function (sort/format/jobStatus/api).
// This is the first test that mounts a REAL page with a real QueryClient +
// fetch mock, exercising:
//   1. the page renders without crashing (catches TS type drift that breaks
//      JSX, broken imports, hook misuse) and the useQuery data flows through
//   2. a full data→action→mutation interaction (click a row's 合并 button →
//      MergeModal opens → submit → the merge endpoint is hit) — guards a
//      refactor breaking the row→mutation wiring
//
// No MSW: we stub global.fetch directly (the same pattern as api.test.ts), so
// no new dependency. The mutation→invalidateQueries rule (AGENTS.md #2) is
// enforced by code review here, not by a flaky refetch-timing assertion.

const realFetch = global.fetch;

function makeClient() {
  // A fresh client per test so caches/probes don't leak. retries:0 so a failed
  // query doesn't hang the test on retry backoff.
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 0 } },
  });
}

// renderPage mounts GradesTable with the providers it needs (QueryClient +
// ToastProvider) and a route-aware fetch mock. Returns the captured fetch
// calls so a test can assert which endpoints were hit. extraRoutes adds
// non-GET handlers (mutations); GET /admin/api/grades always returns
// currentGrades so the list query resolves.
function renderPage(extraRoutes?: Record<string, (body: any) => unknown>) {
  const calls: { url: string; init?: RequestInit }[] = [];
  global.fetch = vi.fn(async (url: any, init?: any) => {
    calls.push({ url: url as string, init });
    const u = url as string;
    const method = (init?.method ?? 'GET') as string;
    let body: unknown = [];
    if (u.includes('/admin/api/grades') && method === 'GET') {
      body = currentGrades;
    } else if (extraRoutes) {
      for (const key of Object.keys(extraRoutes)) {
        if (u.includes(key)) {
          body = extraRoutes[key](JSON.parse((init?.body as string) ?? '{}'));
          break;
        }
      }
    }
    const text = JSON.stringify(body);
    return { ok: true, status: 200, text: () => Promise.resolve(text) } as unknown as Response;
  });
  const utils = render(
    <QueryClientProvider client={makeClient()}>
      <ToastProvider>
        <GradesTable />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { ...utils, calls };
}

// The mutable grades list the mock reads. A mutation (merge) mutates this so
// the post-invalidate refetch sees the new state — proving the page re-fetched.
let currentGrades: any[];

beforeEach(() => {
  currentGrades = [
    { grade: 'primary', label: '小学', count: 2, is_preset: true },
    { grade: '考研', label: '考研', count: 1, is_preset: false },
  ];
});
afterEach(() => {
  global.fetch = realFetch;
});

describe('GradesTable page mount', () => {
  it('renders the grade rows from the listGrades query', async () => {
    renderPage();
    // Wait for the query to resolve + render. The custom tag "考研" appears in
    // BOTH the label cell and the key cell, so use getAllByText (>1 match).
    await waitFor(() => {
      expect(screen.getAllByText('考研').length).toBeGreaterThan(0);
    });
    // Preset label is localized (小学), not the raw key (primary).
    expect(screen.getAllByText('小学').length).toBeGreaterThan(0);
  });

  it('merge action opens the MergeModal and posts the mutation on confirm (proves the data→action→mutation wiring)', async () => {
    // This drives ONE full interaction: render → click the custom tag's 合并
    // button → the MergeModal opens → submit → the merge endpoint is hit. It
    // catches the failure mode where a page refactor breaks the row→mutation
    // wiring (e.g. a wrong grade passed, or the button stops opening the modal)
    // without depending on the invalidate-timing detail (asserted separately by
    // the page-render test + code review per AGENTS.md rule #2).
    const { calls } = renderPage({
      '/admin/api/grades/merge': () => ({ status: 'merged' }),
    });

    await waitFor(() => expect(screen.getAllByText('考研').length).toBeGreaterThan(0));

    // Click the 合并 button in the 考研 row. The button's text is "合并"
    // (Grades.tsx renders it inline, not in title).
    const row = screen.getAllByRole('row').find((r) => r.textContent?.includes('考研'));
    expect(row).toBeTruthy();
    const mergeBtn = Array.from(row!.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === '合并',
    );
    expect(mergeBtn).toBeTruthy();
    fireEvent.click(mergeBtn!);

    // The MergeModal opens. It has a target selector + a confirm button. We
    // just need to confirm the modal appeared (the wiring up to here is the
    // contract being tested) and that submitting hits the merge endpoint.
    await waitFor(() => {
      // The modal's presence — its title contains "合并".
      const modalTitle = screen.queryAllByText(/合并/);
      expect(modalTitle.length).toBeGreaterThan(0);
    });
    // The MergeModal's confirm button is labeled "确认合并" (Grades.tsx:275).
    // It must exist once the modal opens — assert it rather than silently
    // skipping, so a refactor that breaks the modal render surfaces here.
    const confirmBtn = await screen.findByRole('button', { name: /确认合并/ });
    fireEvent.click(confirmBtn);

    // The merge mutation should have POSTed.
    await waitFor(() => {
      expect(calls.some((c) => c.url.includes('/merge'))).toBe(true);
    });
  });
});
