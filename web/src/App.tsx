import { Component, type ErrorInfo, type ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "./lib/auth";
import { ToastProvider } from "./lib/toast";
import { Layout } from "./components/Layout";
import { LoadingBlock } from "./components/ui";
import { LoginPage } from "./components/Layout";
import { OverviewPage } from "./pages/Overview";
import { InstancesPage } from "./pages/Instances";
import { CreateInstancePage } from "./pages/CreateInstance";
import { InstanceDetailPage } from "./pages/InstanceDetail";
import { DomainsPage } from "./pages/Domains";
import { CaddyPage } from "./pages/Caddy";
import { SMTPPage } from "./pages/SMTP";
import { StoragePage } from "./pages/Storage";
import { BackupsPage } from "./pages/Backups";
import { MonitoringPage } from "./pages/Monitoring";
import { JobsPage } from "./pages/Jobs";
import { AuditPage } from "./pages/Audit";
import { SettingsPage } from "./pages/Settings";
import { AdminsPage } from "./pages/Admins";

function ProtectedLayout() {
  const { admin, loading } = useAuth();
  const location = useLocation();
  if (loading) {
    return (
      <div className="login-loading">
        <LoadingBlock label="Checking session…" />
      </div>
    );
  }
  if (!admin) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return <Layout />;
}

class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Dashboard render error", error, info);
  }
  render() {
    if (this.state.error) {
      return (
        <div className="fatal-screen">
          <h1>Dashboard error</h1>
          <p>{this.state.error.message}</p>
          <button className="button button-primary button-md" onClick={() => window.location.reload()}>
            Reload
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

export default function App() {
  return (
    <ErrorBoundary>
      <BrowserRouter>
        <AuthProvider>
          <ToastProvider>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route element={<ProtectedLayout />}>
                <Route index element={<OverviewPage />} />
                <Route path="instances" element={<InstancesPage />} />
                <Route path="instances/new" element={<CreateInstancePage />} />
                <Route path="instances/:id" element={<InstanceDetailPage />} />
                <Route path="domains" element={<DomainsPage />} />
                <Route path="caddy" element={<CaddyPage />} />
                <Route path="smtp" element={<SMTPPage />} />
                <Route path="storage" element={<StoragePage />} />
                <Route path="backups" element={<BackupsPage />} />
                <Route path="monitoring" element={<MonitoringPage />} />
                <Route path="jobs" element={<JobsPage />} />
                <Route path="audit" element={<AuditPage />} />
                <Route path="settings" element={<SettingsPage />} />
                <Route path="admins" element={<AdminsPage />} />
                <Route path="*" element={<NotFoundPage />} />
              </Route>
            </Routes>
          </ToastProvider>
        </AuthProvider>
      </BrowserRouter>
    </ErrorBoundary>
  );
}

function NotFoundPage() {
  return (
    <div className="not-found">
      <span className="eyebrow">404</span>
      <h1>Page not found</h1>
      <p>The requested dashboard page does not exist.</p>
      <a href="/" className="button button-primary button-md">
        Back to overview
      </a>
    </div>
  );
}
