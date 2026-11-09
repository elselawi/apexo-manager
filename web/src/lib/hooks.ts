import { useCallback, useEffect, useRef, useState } from "react";

export function useDocumentTitle(title: string): void {
  useEffect(() => {
    document.title = title ? `${title} · PocketBase Manager` : "PocketBase Manager";
  }, [title]);
}

export function usePolling<T>(
  loader: (signal: AbortSignal) => Promise<T>,
  intervalMs: number | null,
  deps: unknown[] = [],
): {
  data: T | null;
  error: string | null;
  loading: boolean;
  refresh: () => void;
} {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [nonce, setNonce] = useState(0);
  const loaderRef = useRef(loader);
  loaderRef.current = loader;

  const refresh = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    async function run(initial: boolean) {
      if (initial) setLoading(true);
      try {
        const result = await loaderRef.current(controller.signal);
        if (!active) return;
        setData(result);
        setError(null);
      } catch (err) {
        if (!active || controller.signal.aborted) return;
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (active && initial) setLoading(false);
      }
    }
    void run(true);
    let timer: number | undefined;
    if (intervalMs && intervalMs > 0) {
      timer = window.setInterval(() => void run(false), intervalMs);
    }
    return () => {
      active = false;
      controller.abort();
      if (timer) window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, nonce, ...deps]);

  return { data, error, loading, refresh };
}

export function useAsyncAction(): {
  busy: boolean;
  error: string | null;
  run: <T>(fn: () => Promise<T>) => Promise<T | null>;
  clearError: () => void;
} {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = useCallback(async <T,>(fn: () => Promise<T>): Promise<T | null> => {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      return null;
    } finally {
      setBusy(false);
    }
  }, []);
  return { busy, error, run, clearError: () => setError(null) };
}

export function useCopy(): [string | null, (text: string, label?: string) => void] {
  const [copied, setCopied] = useState<string | null>(null);
  const copy = useCallback((text: string, label = "Copied") => {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(label);
      window.setTimeout(() => setCopied(null), 1800);
    });
  }, []);
  return [copied, copy];
}

export function useDebouncedValue<T>(value: T, delayMs = 300): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delayMs);
    return () => window.clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}
