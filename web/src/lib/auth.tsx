import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "./api";
import type { Admin } from "../types";

interface AuthContextValue {
  admin: Admin | null;
  csrfToken: string;
  loading: boolean;
  initialized: boolean;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
  setAdmin: (admin: Admin) => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [admin, setAdmin] = useState<Admin | null>(null);
  const [csrfToken, setCsrfToken] = useState("");
  const [loading, setLoading] = useState(true);
  const [initialized, setInitialized] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const me = await api.me();
      api.setCSRF(me.csrfToken);
      setCSRFState(me.csrfToken, setCsrfToken);
      setAdmin(me.admin);
    } catch {
      api.setCSRF("");
      setCSRFState("", setCsrfToken);
      setAdmin(null);
    }
  }, []);

  useEffect(() => {
    let active = true;
    (async () => {
      try {
        const status = await api.authStatus();
        if (active) setInitialized(status.initialized);
      } catch {
        // The login page still renders and will show the connection error.
      }
      await refresh();
      if (active) setLoading(false);
    })();
    return () => {
      active = false;
    };
  }, [refresh]);

  useEffect(() => {
    const onUnauthorized = () => {
      api.setCSRF("");
      setCSRFState("", setCsrfToken);
      setAdmin(null);
    };
    window.addEventListener("pbmanager:unauthorized", onUnauthorized);
    return () => window.removeEventListener("pbmanager:unauthorized", onUnauthorized);
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const result = await api.login(username, password);
    api.setCSRF(result.csrfToken);
    setCSRFState(result.csrfToken, setCsrfToken);
    setAdmin(result.admin);
    setInitialized(true);
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      api.setCSRF("");
      setCSRFState("", setCsrfToken);
      setAdmin(null);
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ admin, csrfToken, loading, initialized, login, logout, refresh, setAdmin }),
    [admin, csrfToken, loading, initialized, login, logout, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

function setCSRFState(token: string, setter: (token: string) => void) {
  setter(token);
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used within AuthProvider");
  return context;
}
