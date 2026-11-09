import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { CheckCircle2, AlertTriangle, Info, X, XCircle } from "lucide-react";

type ToastTone = "success" | "error" | "warning" | "info";
interface Toast {
  id: number;
  tone: ToastTone;
  title: string;
  message?: string;
}

interface ToastContextValue {
  push: (tone: ToastTone, title: string, message?: string) => void;
  success: (title: string, message?: string) => void;
  error: (title: string, message?: string) => void;
  warning: (title: string, message?: string) => void;
  info: (title: string, message?: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);
let counter = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const remove = useCallback((id: number) => {
    setToasts((list) => list.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (tone: ToastTone, title: string, message?: string) => {
      const id = ++counter;
      setToasts((list) => [...list.slice(-4), { id, tone, title, message }]);
      window.setTimeout(() => remove(id), tone === "error" ? 8000 : 4500);
    },
    [remove],
  );

  const value = useMemo<ToastContextValue>(
    () => ({
      push,
      success: (title, message) => push("success", title, message),
      error: (title, message) => push("error", title, message),
      warning: (title, message) => push("warning", title, message),
      info: (title, message) => push("info", title, message),
    }),
    [push],
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="toast-stack" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast toast-${toast.tone}`}>
            <span className="toast-icon">{toastIcon(toast.tone)}</span>
            <div className="toast-content">
              <strong>{toast.title}</strong>
              {toast.message ? <span>{toast.message}</span> : null}
            </div>
            <button className="icon-button subtle" onClick={() => remove(toast.id)} aria-label="Dismiss">
              <X size={15} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

function toastIcon(tone: ToastTone) {
  switch (tone) {
    case "success":
      return <CheckCircle2 size={18} />;
    case "error":
      return <XCircle size={18} />;
    case "warning":
      return <AlertTriangle size={18} />;
    default:
      return <Info size={18} />;
  }
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext);
  if (!context) throw new Error("useToast must be used within ToastProvider");
  return context;
}
