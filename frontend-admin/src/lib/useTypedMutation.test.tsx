import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ToastProvider } from './toast';
import { useTypedMutation } from './useTypedMutation';

// useTypedMutation wraps useMutation with:
//   - success toast (optional `successMsg`)
//   - query invalidation (via `invalidateKeys` / `invalidateFn`)
//   - error toast (always, using the thrown message or `errorMsg` fallback)
//
// These tests pin that contract: the success path invalidates + toasts, the
// error path surfaces the thrown message (or fallback) without throwing out
// of `mutate`, and the zero-arg overload is callable with `.mutate()` /
// `.mutateAsync()` with no argument.
//
// Toasts are observed via the DOM: the real ToastProvider renders each toast
// as a `role="alert"` div, so after a mutation we query the document for it
// instead of mocking the toast context. That pins what the user actually sees.

// Build a fresh QueryClient per test so cache state never leaks across cases.
// retries: 0 so mutation failures reject immediately instead of retrying.
function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        <ToastProvider>{children}</ToastProvider>
      </QueryClientProvider>
    );
  };
}

// Read the most-recently-rendered toast message (or '' if none yet).
function latestToast(): string {
  const alerts = document.querySelectorAll('[role="alert"]');
  if (alerts.length === 0) return '';
  return alerts[alerts.length - 1].textContent ?? '';
}

describe('useTypedMutation - success path', () => {
  let client: QueryClient;
  beforeEach(() => {
    client = makeClient();
  });

  it('fires the success toast and invalidates the supplied query keys', async () => {
    const mutationFn = vi.fn().mockResolvedValue({ ok: true });
    const invalidate = vi.spyOn(client, 'invalidateQueries');

    const { result } = renderHook(
      () => useTypedMutation({ mutationFn, successMsg: '已删除', invalidateKeys: [['users'], ['courses']] }),
      { wrapper: makeWrapper(client) },
    );

    await act(async () => {
      await result.current.mutateAsync(42);
    });

    // React Query passes a 2nd context arg ({ client, meta, mutationKey });
    // assert only that the user-facing payload is `42`.
    expect(mutationFn).toHaveBeenCalledWith(42, expect.anything());
    expect(invalidate).toHaveBeenCalledTimes(2);
    expect(latestToast()).toContain('已删除');
    expect(result.current.isPending).toBe(false);
  });

  it('supports the zero-arg overload (mutationFn: () => ...)', async () => {
    const mutationFn = vi.fn().mockResolvedValue('done');
    const { result } = renderHook(
      () => useTypedMutation<void, string>({ mutationFn, successMsg: 'ok', invalidateKeys: [['x']] }),
      { wrapper: makeWrapper(client) },
    );

    await act(async () => {
      // No argument — TVars=void.
      await result.current.mutateAsync();
    });

    // React Query passes a 2nd context arg even for void mutations; the
    // user-facing payload is `undefined`.
    expect(mutationFn).toHaveBeenCalledWith(undefined, expect.anything());
    expect(latestToast()).toContain('ok');
  });

  it('invokes onSuccess with (data, vars) after the toast + invalidation', async () => {
    const mutationFn = vi.fn().mockResolvedValue({ id: 7 });
    const onSuccess = vi.fn();

    const { result } = renderHook(
      () => useTypedMutation({ mutationFn, invalidateKeys: [['x']], successMsg: 'ok', onSuccess }),
      { wrapper: makeWrapper(client) },
    );

    await act(async () => {
      await result.current.mutateAsync('payload');
    });

    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(onSuccess).toHaveBeenCalledWith({ id: 7 }, 'payload');
    // And the toast still fired before onSuccess.
    expect(latestToast()).toContain('ok');
  });
});

describe('useTypedMutation - error path', () => {
  let client: QueryClient;
  beforeEach(() => {
    client = makeClient();
  });

  it('toasts the thrown error message and does not throw out of mutate', async () => {
    const mutationFn = vi.fn().mockRejectedValue(new Error('网络超时'));

    const { result } = renderHook(
      () => useTypedMutation({ mutationFn, errorMsg: '默认失败' }),
      { wrapper: makeWrapper(client) },
    );

    // mutate (fire-and-forget) — should NOT throw synchronously.
    act(() => {
      result.current.mutate(1);
    });

    await waitFor(() => {
      expect(latestToast()).toContain('网络超时');
    });
  });

  it('falls back to errorMsg when the thrown error has an empty message', async () => {
    const mutationFn = vi.fn().mockRejectedValue(new Error(''));

    const { result } = renderHook(
      () => useTypedMutation({ mutationFn, errorMsg: '默认失败' }),
      { wrapper: makeWrapper(client) },
    );

    act(() => {
      result.current.mutate(1);
    });

    await waitFor(() => {
      // Empty message falls through (via `||`) to the errorMsg fallback.
      expect(latestToast()).toContain('默认失败');
    });
  });

  it('falls back to the generic "操作失败" when no errorMsg is supplied', async () => {
    const mutationFn = vi.fn().mockRejectedValue(new Error(''));

    const { result } = renderHook(
      () => useTypedMutation({ mutationFn }),
      { wrapper: makeWrapper(client) },
    );

    act(() => {
      result.current.mutate(1);
    });

    await waitFor(() => {
      expect(latestToast()).toContain('操作失败');
    });
  });
});
