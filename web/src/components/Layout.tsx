import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  Archive,
  Boxes,
  ChevronsLeft,
  Cloud,
  Database,
  FileClock,
  Globe2,
  HardDrive,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  Network,
  ScrollText,
  Server,
  Settings,
  ShieldCheck,
  UserCog,
  X,
} from "lucide-react";
import { useAuth } from "../lib/auth";
import { api, errorMessage } from "../lib/api";
import { useToast } from "../lib/toast";
import { formatDate, formatRelative, titleCase } from "../lib/format";
import { Badge, Button, Field, Input, LoadingBlock, Modal, StatusBadge } from "./ui";
import type { Session } from "../types";

const navGroups = [
  {
    label: "Operations",
    items: [
      { to: "/", label: "Overview", icon: LayoutDashboard, end: true },
      { to: "/instances", label: "PocketBase Instances", icon: Boxes },
      { to: "/domains", label: "Domains & DNS", icon: Globe2 },
      { to: "/caddy", label: "Caddy", icon: Network },
    ],
  },
  {
    label: "Configuration",
    items: [
      { to: "/smtp", label: "SMTP Profiles", icon: Cloud },
      { to: "/storage", label: "S3 Storage", icon: HardDrive },
      { to: "/backups", label: "Backups", icon: Archive },
    ],
  },
  {
    label: "Observability",
    items: [
      { to: "/monitoring", label: "Monitoring", icon: Activity },
      { to: "/jobs", label: "Background Jobs", icon: FileClock },
      { to: "/audit", label: "Activity & Audit", icon: ScrollText },
    ],
  },
  {
    label: "System",
    items: [
      { to: "/settings", label: "Server Configuration", icon: Settings, ownerOnly: true },
      { to: "/admins", label: "Administrators", icon: UserCog, ownerOnly: true },
    ],
  },
];

const pageTitles: [RegExp, string][] = [
  [/^\/$/, "Overview"],
  [/^\/instances\/new/, "Provision instance"],
  [/^\/instances\/[^/]+/, "Instance detail"],
  [/^\/instances/, "PocketBase Instances"],
  [/^\/domains/, "Domains & DNS"],
  [/^\/caddy/, "Caddy"],
  [/^\/smtp/, "SMTP Profiles"],
  [/^\/storage/, "S3 Storage"],
  [/^\/backups/, "Backups"],
  [/^\/monitoring/, "Monitoring"],
  [/^\/jobs/, "Background Jobs"],
  [/^\/audit/, "Activity & Audit"],
  [/^\/settings/, "Server Configuration"],
  [/^\/admins/, "Administrators"],
];

export function Layout() {
  const { admin, logout } = useAuth();
  const location = useLocation();
  const navigate = useNavigate();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [accountOpen, setAccountOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    setMobileOpen(false);
  }, [location.pathname]);

  const pageTitle = pageTitles.find(([pattern]) => pattern.test(location.pathname))?.[1] ?? "PocketBase Manager";

  async function handleLogout() {
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div className={`app-shell ${collapsed ? "sidebar-collapsed" : ""}`}>
      <aside className={`sidebar ${mobileOpen ? "sidebar-open" : ""}`}>
        <div className="sidebar-brand">
          <div className="brand-mark">
            <Database size={21} />
          </div>
          <div className="brand-copy">
            <strong>PocketBase</strong>
            <span>Manager</span>
          </div>
          <button className="sidebar-close" onClick={() => setMobileOpen(false)} aria-label="Close navigation">
            <X size={18} />
          </button>
        </div>
        <nav className="sidebar-nav" aria-label="Primary">
          {navGroups.map((group) => {
            const items = group.items.filter((item) => !("ownerOnly" in item && item.ownerOnly) || admin?.role === "owner");
            if (items.length === 0) return null;
            return (
              <div className="nav-group" key={group.label}>
                <div className="nav-group-label">{group.label}</div>
                {items.map((item) => {
                  const Icon = item.icon;
                  return (
                    <NavLink
                      key={item.to}
                      to={item.to}
                      end={"end" in item ? item.end : false}
                      className={({ isActive }) => `nav-item ${isActive ? "nav-active" : ""}`}
                      title={item.label}
                    >
                      <Icon size={18} />
                      <span>{item.label}</span>
                    </NavLink>
                  );
                })}
              </div>
            );
          })}
        </nav>
        <div className="sidebar-footer">
          <div className="sidebar-status">
            <span className="pulse-dot" />
            <span>Manager online</span>
          </div>
          <button className="collapse-button" onClick={() => setCollapsed((value) => !value)}>
            <ChevronsLeft size={16} />
            <span>Collapse</span>
          </button>
        </div>
      </aside>
      {mobileOpen ? <button className="sidebar-scrim" onClick={() => setMobileOpen(false)} aria-label="Close navigation" /> : null}
      <div className="main-shell">
        <header className="topbar">
          <div className="topbar-left">
            <button className="mobile-menu" onClick={() => setMobileOpen(true)} aria-label="Open navigation">
              <Menu size={20} />
            </button>
            <span className="topbar-title">{pageTitle}</span>
          </div>
          <div className="topbar-right">
            <div className="topbar-environment">
              <Server size={14} />
              <span>Self-hosted</span>
            </div>
            <button className="user-menu-button" onClick={() => setAccountOpen(true)}>
              <span className="avatar">{(admin?.displayName || admin?.username || "?").slice(0, 2).toUpperCase()}</span>
              <span className="user-copy">
                <strong>{admin?.displayName || admin?.username}</strong>
                <span>{titleCase(admin?.role)}</span>
              </span>
            </button>
            <button className="logout-button" onClick={() => void handleLogout()} aria-label="Sign out" title="Sign out">
              <LogOut size={17} />
            </button>
          </div>
        </header>
        <main className="main-content">
          <Outlet />
        </main>
      </div>
      <AccountModal open={accountOpen} onClose={() => setAccountOpen(false)} />
    </div>
  );
}

function AccountModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { admin, refresh } = useAuth();
  const toast = useToast();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setSessionsLoading(true);
    api
      .ownSessions()
      .then((result) => setSessions(result.items))
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setSessionsLoading(false));
  }, [open]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (newPassword !== confirmPassword) {
      setError("New passwords do not match");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.changePassword(currentPassword, newPassword);
      toast.success("Password changed", "Other sessions have been revoked.");
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      await refresh();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(id: string) {
    try {
      await api.revokeOwnSession(id);
      setSessions((list) => list.map((s) => (s.id === id ? { ...s, revokedAt: new Date().toISOString() } : s)));
      toast.success("Session revoked");
    } catch (err) {
      toast.error("Could not revoke session", errorMessage(err));
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Administrator account" description="Password and active sessions" size="lg">
      <div className="account-grid">
        <div>
          <div className="account-profile">
            <span className="avatar large">{(admin?.displayName || admin?.username || "?").slice(0, 2).toUpperCase()}</span>
            <div>
              <strong>{admin?.displayName}</strong>
              <div className="muted">@{admin?.username}</div>
              <Badge tone="info">{titleCase(admin?.role)}</Badge>
            </div>
          </div>
          <form onSubmit={(event) => void submit(event)} className="stack">
            <h3>Change password</h3>
            {error ? <div className="form-error">{error}</div> : null}
            <Field label="Current password">
              <Input
                type="password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
                autoComplete="current-password"
                required
                autoFocus
              />
            </Field>
            <Field label="New password" hint="At least 12 characters.">
              <Input
                type="password"
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
                autoComplete="new-password"
                minLength={12}
                required
              />
            </Field>
            <Field label="Confirm new password">
              <Input
                type="password"
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
                autoComplete="new-password"
                required
              />
            </Field>
            <Button type="submit" variant="primary" loading={busy}>
              Update password
            </Button>
          </form>
        </div>
        <div>
          <h3>Active sessions</h3>
          {sessionsLoading ? (
            <LoadingBlock label="Loading sessions…" />
          ) : (
            <div className="session-list">
              {sessions.map((session, index) => (
                <div className="session-row" key={`${session.createdAt}-${index}`}>
                  <div>
                    <strong>{session.ip || "Unknown IP"}</strong>
                    <span className="muted">{session.userAgent || "Unknown client"}</span>
                    <span className="muted">
                      Created {formatDate(session.createdAt)} · seen {formatRelative(session.lastSeenAt)}
                    </span>
                  </div>
                  {session.revokedAt ? (
                    <StatusBadge status="revoked" label="Revoked" />
                  ) : (
                    <Button size="sm" variant="ghost" onClick={() => void revoke(session.id)}>
                      Revoke
                    </Button>
                  )}
                </div>
              ))}
            </div>
          )}
          <div className="security-note">
            <ShieldCheck size={16} />
            <span>If you lose access, use root CLI: <code>sudo pbmanager admin reset-password</code></span>
          </div>
        </div>
      </div>
    </Modal>
  );
}

export function LoginPage() {
  const { login, admin, loading, initialized } = useAuth();
  const navigate = useNavigate();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();

  useEffect(() => {
    if (!loading && admin) navigate("/", { replace: true });
  }, [admin, loading, navigate]);

  if (loading) {
    return (
      <div className="login-loading">
        <LoadingBlock label="Loading manager…" />
      </div>
    );
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await login(username, password);
      toast.success("Signed in");
      navigate("/", { replace: true });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="login-page">
      <section className="login-brand-panel">
        <div className="login-brand">
          <div className="brand-mark large">
            <Database size={28} />
          </div>
          <div>
            <strong>PocketBase</strong>
            <span>Infrastructure Manager</span>
          </div>
        </div>
        <div className="login-pitch">
          <h1>One control plane for every PocketBase instance.</h1>
          <p>
            Discover installations, manage systemd and Caddy, wire DNS, apply SMTP and S3 profiles, verify backups, and
            keep every site observable — without touching existing data.
          </p>
          <ul>
            <li><ShieldCheck size={17} /> Root-owned recovery stays available through the CLI.</li>
            <li><Network size={17} /> Caddy changes are staged, validated, backed up, and reversible.</li>
            <li><KeyRound size={17} /> Application credentials are encrypted at rest and opt-in.</li>
            <li><Archive size={17} /> Jobs survive browser refreshes, crashes, and reboots.</li>
          </ul>
        </div>
        <div className="login-footnote">PocketBase Manager · self-hosted operations</div>
      </section>
      <section className="login-form-panel">
        <form className="login-form" onSubmit={(event) => void submit(event)}>
          <div className="mobile-brand">
            <span className="brand-mark"><Database size={20} /></span>
            <strong>PocketBase Manager</strong>
          </div>
          <h2>Sign in</h2>
          <p className="muted">Use a dashboard administrator account.</p>
          {!initialized ? (
            <div className="bootstrap-box">
              <strong>No administrator exists yet</strong>
              <p>Create the first owner from the server CLI. There are no default credentials.</p>
              <code>sudo pbmanager admin create</code>
            </div>
          ) : null}
          {error ? <div className="form-error">{error}</div> : null}
          <Field label="Username">
            <Input
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              autoFocus
              required
              placeholder="admin"
            />
          </Field>
          <Field label="Password">
            <Input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              required
            />
          </Field>
          <Button type="submit" variant="primary" loading={busy} className="login-submit">
            Sign in
          </Button>
          <p className="login-help">
            Forgot your password? On the server run <code>sudo pbmanager admin reset-password</code>.
          </p>
        </form>
      </section>
    </div>
  );
}
