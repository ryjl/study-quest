import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';

// Lightweight toast system that replaces window.alert throughout the admin.
type ToastKind = 'success' | 'error' | 'info' | 'warning';
interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

interface ToastCtx {
  push: (kind: ToastKind, message: string) => void;
  success: (m: string) => void;
  error: (m: string) => void;
  info: (m: string) => void;
}

const Ctx = createContext<ToastCtx | null>(null);

export function useToast(): ToastCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error('useToast must be used within ToastProvider');
  return ctx;
}

let counter = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const push = useCallback((kind: ToastKind, message: string) => {
    const id = ++counter;
    setToasts((t) => [...t, { id, kind, message }]);
    setTimeout(() => {
      setToasts((t) => t.filter((x) => x.id !== id));
    }, 4000);
  }, []);

  const api: ToastCtx = {
    push,
    success: (m) => push('success', m),
    error: (m) => push('error', m),
    info: (m) => push('info', m),
  };

  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="fixed top-4 right-4 z-[2000] flex flex-col gap-2 pointer-events-none">
        {toasts.map((t) => (
          <ToastItem key={t.id} toast={t} onClose={() => setToasts((x) => x.filter((y) => y.id !== t.id))} />
        ))}
      </div>
    </Ctx.Provider>
  );
}

const styles: Record<ToastKind, { bg: string; icon: string }> = {
  success: { bg: 'border-good/40 bg-good/10 text-good', icon: '✓' },
  error: { bg: 'border-bad/40 bg-bad/10 text-bad', icon: '✕' },
  info: { bg: 'border-primary/40 bg-primary/10 text-primary', icon: 'ℹ' },
  warning: { bg: 'border-warn/40 bg-warn/10 text-warn', icon: '!' },
};

function ToastItem({ toast, onClose }: { toast: Toast; onClose: () => void }) {
  const s = styles[toast.kind];
  return (
    <div
      className={`pointer-events-auto flex items-center gap-2.5 rounded-xl border px-4 py-3 shadow-lg backdrop-blur min-w-[280px] max-w-md ${s.bg}`}
      role="alert"
    >
      <span className="font-bold">{s.icon}</span>
      <span className="flex-1 text-sm text-txt">{toast.message}</span>
      <button onClick={onClose} className="text-muted hover:text-txt" aria-label="关闭">
        ×
      </button>
    </div>
  );
}

// ConfirmDialog replaces window.confirm with a promise-based modal.
interface ConfirmState {
  message: string;
  detail?: string;
  danger?: boolean;
  resolve: (v: boolean) => void;
}

const ConfirmCtx = createContext<{ confirm: (opts: { message: string; detail?: string; danger?: boolean }) => Promise<boolean> } | null>(null);

export function useConfirm() {
  const ctx = useContext(ConfirmCtx);
  if (!ctx) throw new Error('useConfirm must be used within ConfirmProvider');
  return ctx.confirm;
}

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<ConfirmState | null>(null);

  const confirm = useCallback((opts: { message: string; detail?: string; danger?: boolean }) => {
    return new Promise<boolean>((resolve) => {
      setState({ ...opts, resolve });
    });
  }, []);

  const finish = (v: boolean) => {
    state?.resolve(v);
    setState(null);
  };

  // ESC to cancel
  useEffect(() => {
    if (!state) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') finish(false);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [state]);

  return (
    <ConfirmCtx.Provider value={{ confirm }}>
      {children}
      {state && (
        <div className="fixed inset-0 z-[2100] flex items-center justify-center bg-black/60 backdrop-blur-sm p-4" onClick={() => finish(false)}>
          <div
            className="w-full max-w-md rounded-2xl border border-border bg-card p-6 shadow-2xl"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-lg font-bold text-txt mb-1">{state.message}</h3>
            {state.detail && <p className="text-sm text-muted mb-5 whitespace-pre-line">{state.detail}</p>}
            {!state.detail && <div className="mb-5" />}
            <div className="flex justify-end gap-2">
              <button className="btn-secondary" onClick={() => finish(false)}>
                取消
              </button>
              <button className={state.danger ? 'btn-danger' : 'btn-primary'} onClick={() => finish(true)}>
                确认
              </button>
            </div>
          </div>
        </div>
      )}
    </ConfirmCtx.Provider>
  );
}
