import { useEffect, useState } from "react";
import { KeyRound, Plus, ShieldCheck, Trash2, UserCog, Users } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatDate, formatRelative, titleCase } from "../lib/format";
import type { Admin, Role } from "../types";
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  ErrorBanner,
  Field,
  IconButton,
  Input,
  LoadingBlock,
  Modal,
  Notice,
  PageHeader,
  Select,
  StatusBadge,
  Switch,
} from "../components/ui";

export function AdminsPage() {
  useDocumentTitle("Administrators");
  const { admin: me } = useAuth();
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.admins(), 20000);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Admin | null>(null);
  const [deleting, setDeleting] = useState<Admin | null>(null);
  const [revoking, setRevoking] = useState<Admin | null>(null);

  if (me?.role !== "owner") {
    return (
      <div className="page">
        <PageHeader eyebrow="System" title="Administrators" description="Only the owner role can manage dashboard administrators." />
        <Notice tone="warning" title="Owner permission required">Use the root CLI if you need to recover access.</Notice>
      </div>
    );
  }

  const admins = data?.items ?? [];

  return (
    <div className="page">
      <PageHeader
        eyebrow="System"
        title="Administrators"
        description="Dashboard accounts and roles. Root-level CLI recovery always remains available independently."
        actions={<Button variant="primary" icon={<Plus size={16} />} onClick={() => { setEditing(null); setModalOpen(true); }}>New administrator</Button>}
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      <Notice tone="info" title="CLI remains the recovery authority">
        If every dashboard account is lost or the web service is down, root can create or reset administrators with
        <code> sudo pbmanager admin create</code> and <code>sudo pbmanager admin reset-password</code>.
      </Notice>

      <Card title="Dashboard accounts" subtitle="Strong passwords, Argon2id hashing, revocable sessions." padded={false}>
        {loading && !data ? <LoadingBlock /> : admins.length === 0 ? (
          <EmptyState icon={<Users size={26} />} title="No administrators" description="Use the CLI to bootstrap the first owner." />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead><tr><th>Administrator</th><th>Role</th><th>Status</th><th>Last login</th><th>Created</th><th /></tr></thead>
              <tbody>
                {admins.map((admin) => (
                  <tr key={admin.id}>
                    <td>
                      <div className="table-primary">
                        <span className="avatar small">{admin.displayName.slice(0, 2).toUpperCase()}</span>
                        <span><strong>{admin.displayName}{admin.id === me?.id ? " (you)" : ""}</strong><small>@{admin.username}</small></span>
                      </div>
                    </td>
                    <td><Badge tone={admin.role === "owner" ? "info" : admin.role === "admin" ? "success" : "neutral"}>{titleCase(admin.role)}</Badge></td>
                    <td><StatusBadge status={admin.disabled ? "disabled" : "active"} label={admin.disabled ? "Disabled" : "Active"} /></td>
                    <td className="muted">{admin.lastLoginAt ? formatRelative(admin.lastLoginAt) : "never"}</td>
                    <td className="muted">{formatDate(admin.createdAt)}</td>
                    <td className="table-actions">
                      <Button size="sm" variant="ghost" onClick={() => { setEditing(admin); setModalOpen(true); }}>Edit</Button>
                      <Button size="sm" variant="ghost" onClick={() => setRevoking(admin)}>Revoke sessions</Button>
                      {admin.id !== me?.id ? <IconButton label="Delete administrator" onClick={() => setDeleting(admin)}><Trash2 size={15} /></IconButton> : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <div className="two-column-grid">
        <Card title="Roles" subtitle="Least privilege by default">
          <div className="role-grid">
            <div><Badge tone="info">Owner</Badge><p>Full control including server configuration and administrator management. At least one enabled owner must remain.</p></div>
            <div><Badge tone="success">Admin</Badge><p>Manages instances, domains, Caddy, DNS, profiles, backups, and jobs. Cannot change server-wide settings or administrators.</p></div>
            <div><Badge tone="neutral">Viewer</Badge><p>Read-only visibility into infrastructure, logs, jobs, monitoring, and audit history.</p></div>
          </div>
        </Card>
        <Card title="Root recovery" subtitle="No lockout by design">
          <ul className="feature-list">
            <li><KeyRound size={16} /> Reset a password: <code>sudo pbmanager admin reset-password</code></li>
            <li><Plus size={16} /> Create an owner: <code>sudo pbmanager admin create</code></li>
            <li><ShieldCheck size={16} /> Revoke sessions: <code>sudo pbmanager admin revoke-sessions</code></li>
            <li><UserCog size={16} /> The CLI works with the web service stopped.</li>
          </ul>
        </Card>
      </div>

      <AdminModal
        open={modalOpen}
        admin={editing}
        onClose={() => setModalOpen(false)}
        onSaved={() => { refresh(); toast.success("Administrator saved"); }}
      />
      <ConfirmDialog
        open={Boolean(deleting)}
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          if (!deleting) return;
          void api.deleteAdmin(deleting.id)
            .then(() => { toast.success("Administrator deleted"); setDeleting(null); refresh(); })
            .catch((err) => toast.error("Could not delete administrator", errorMessage(err)));
        }}
        title="Delete administrator"
        message={<>Delete <strong>{deleting?.username}</strong>? Their sessions are revoked immediately. The last enabled owner cannot be deleted.</>}
        confirmLabel="Delete administrator"
        danger
        requireText={deleting?.username}
      />
      <ConfirmDialog
        open={Boolean(revoking)}
        onClose={() => setRevoking(null)}
        onConfirm={() => {
          if (!revoking) return;
          void api.revokeAdminSessions(revoking.id)
            .then(() => { toast.success("Sessions revoked"); setRevoking(null); refresh(); })
            .catch((err) => toast.error("Could not revoke sessions", errorMessage(err)));
        }}
        title="Revoke sessions"
        message={<>Revoke every active session for <strong>{revoking?.username}</strong>? They will need to sign in again.</>}
        confirmLabel="Revoke sessions"
      />
    </div>
  );
}

function AdminModal({
  open,
  admin,
  onClose,
  onSaved,
}: {
  open: boolean;
  admin: Admin | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState({ username: "", displayName: "", role: "admin", password: "", disabled: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setForm({
      username: admin?.username ?? "",
      displayName: admin?.displayName ?? "",
      role: admin?.role ?? "admin",
      password: "",
      disabled: admin?.disabled ?? false,
    });
  }, [open, admin]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (admin) {
        await api.updateAdmin(admin.id, { displayName: form.displayName, role: form.role, disabled: form.disabled });
      } else {
        await api.createAdmin({ username: form.username, displayName: form.displayName, password: form.password, role: form.role });
      }
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={admin ? "Edit administrator" : "Create administrator"}
      description={admin ? "Role changes and disabled accounts revoke active sessions." : "Passwords are hashed with Argon2id and never exposed."}
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" type="submit" form="admin-form" loading={busy}>Save administrator</Button></>}
    >
      <form id="admin-form" className="stack" onSubmit={(event) => void submit(event)}>
        {error ? <div className="form-error">{error}</div> : null}
        {!admin ? (
          <Field label="Username" required hint="3–64 letters, digits, dots, underscores, @, or hyphens.">
            <Input value={form.username} onChange={(event) => setForm({ ...form, username: event.target.value })} autoComplete="username" required />
          </Field>
        ) : (
          <Field label="Username"><Input value={form.username} disabled /></Field>
        )}
        <Field label="Display name">
          <Input value={form.displayName} onChange={(event) => setForm({ ...form, displayName: event.target.value })} />
        </Field>
        <Field label="Role">
          <Select value={form.role} onChange={(event) => setForm({ ...form, role: event.target.value })}>
            <option value="owner">Owner</option>
            <option value="admin">Admin</option>
            <option value="viewer">Viewer</option>
          </Select>
        </Field>
        {!admin ? (
          <Field label="Password" required hint="At least 12 characters. Prompt-free dashboard creation is allowed only for owners.">
            <Input type="password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} autoComplete="new-password" minLength={12} required />
          </Field>
        ) : (
          <>
            <Switch checked={form.disabled} onChange={(value) => setForm({ ...form, disabled: value })} label="Disable account" description="Disabled administrators cannot sign in and active sessions are revoked." />
            <Notice tone="info">Use the root CLI to reset another administrator&apos;s password: <code>sudo pbmanager admin reset-password</code>.</Notice>
          </>
        )}
      </form>
    </Modal>
  );
}
