import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Cloud, Mail, Plus, Send, Trash2, Wifi } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatRelative } from "../lib/format";
import type { SMTPProfile } from "../types";
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

const emptyForm = {
  name: "",
  host: "",
  port: 587,
  username: "",
  password: "",
  authMethod: "PLAIN",
  tlsMode: "starttls",
  localName: "",
  fromAddress: "",
  fromName: "",
};

export function SMTPPage() {
  useDocumentTitle("SMTP Profiles");
  const { admin } = useAuth();
  const canWrite = admin?.role !== "viewer";
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.smtpProfiles(), 20000);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<SMTPProfile | null>(null);
  const [deleteProfile, setDeleteProfile] = useState<SMTPProfile | null>(null);
  const [testing, setTesting] = useState<string | null>(null);

  const profiles = data?.items ?? [];

  async function test(profile: SMTPProfile) {
    setTesting(profile.id);
    try {
      const result = await api.testSMTPProfile(profile.id);
      if (result.ok) toast.success("SMTP connection and authentication succeeded", `${profile.host}:${profile.port}`);
      else toast.error("SMTP test failed", result.error);
      refresh();
    } catch (err) {
      toast.error("SMTP test failed", errorMessage(err));
    } finally {
      setTesting(null);
    }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Application settings"
        title="SMTP profiles"
        description="Reusable mail configuration with independent connectivity tests. Assignment to an instance does not apply it."
        actions={canWrite ? <Button variant="primary" icon={<Plus size={16} />} onClick={() => { setEditing(null); setModalOpen(true); }}>New SMTP profile</Button> : null}
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      <Notice tone="info" title="Application requires a linked PocketBase superuser">
        Profiles can be created and tested at the infrastructure level. Applying a profile uses PocketBase&apos;s supported
        settings API and requires per-instance credentials. The manager never claims SMTP is operational merely because a
        profile exists.
      </Notice>

      {loading && !data ? <LoadingBlock /> : profiles.length === 0 ? (
        <Card>
          <EmptyState
            icon={<Mail size={28} />}
            title="No SMTP profiles"
            description="Create a profile for your provider, test its credentials, then assign it to instances and apply explicitly."
            action={canWrite ? <Button variant="primary" onClick={() => setModalOpen(true)}>Create profile</Button> : undefined}
          />
        </Card>
      ) : (
        <div className="profile-grid">
          {profiles.map((profile) => (
            <Card
              key={profile.id}
              title={<span className="profile-title"><span className="profile-icon"><Mail size={17} /></span>{profile.name}</span>}
              subtitle={`${profile.host}:${profile.port}`}
              actions={<StatusBadge status={profile.status} />}
            >
              <div className="profile-meta-grid">
                <div><span>TLS</span><strong>{profile.tlsMode === "starttls" ? "STARTTLS" : profile.tlsMode === "tls" ? "Implicit TLS" : "None"}</strong></div>
                <div><span>Auth</span><strong>{profile.authMethod}</strong></div>
                <div><span>Sender</span><strong>{profile.fromAddress || "—"}</strong></div>
                <div><span>Assigned to</span><strong>{profile.usageCount ?? 0} instances</strong></div>
              </div>
              {profile.fromName ? <div className="muted small">Sender name: {profile.fromName}</div> : null}
              {profile.lastError ? <div className="form-error">{profile.lastError}</div> : null}
              <div className="profile-footer">
                <span className="muted small">Tested {formatRelative(profile.lastTestedAt)}</span>
                <div className="button-row">
                  {canWrite ? <Button size="sm" variant="ghost" icon={<Wifi size={14} />} onClick={() => void test(profile)} loading={testing === profile.id}>Test</Button> : null}
                  {canWrite ? <Button size="sm" variant="ghost" onClick={() => { setEditing(profile); setModalOpen(true); }}>Edit</Button> : null}
                  {canWrite ? <IconButton label="Delete profile" onClick={() => setDeleteProfile(profile)}><Trash2 size={15} /></IconButton> : null}
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

      <Card title="How SMTP is applied" subtitle="Status is deliberately explicit at every stage.">
        <div className="pipeline-stages">
          <div><span>1</span><strong>Profile created</strong><p>Credentials are encrypted at rest and never returned.</p></div>
          <div><span>2</span><strong>Credentials tested</strong><p>TCP, TLS, authentication, and NOOP are verified directly.</p></div>
          <div><span>3</span><strong>Applied</strong><p>PocketBase accepts a partial settings PATCH using its public API.</p></div>
          <div><span>4</span><strong>Read-back verified</strong><p>The manager reads settings again and matches host, port, and enabled state.</p></div>
        </div>
        <div className="button-row">
          <Link to="/instances" className="button button-secondary button-md"><Cloud size={15} /> Assign & apply on an instance</Link>
        </div>
      </Card>

      <SMTPFormModal
        open={modalOpen}
        profile={editing}
        onClose={() => setModalOpen(false)}
        onSaved={() => { refresh(); toast.success("SMTP profile saved"); }}
      />
      <ConfirmDialog
        open={Boolean(deleteProfile)}
        onClose={() => setDeleteProfile(null)}
        onConfirm={() => {
          if (!deleteProfile) return;
          void api.deleteSMTPProfile(deleteProfile.id)
            .then(() => { toast.success("SMTP profile deleted"); setDeleteProfile(null); refresh(); })
            .catch((err) => toast.error("Could not delete profile", errorMessage(err)));
        }}
        title="Delete SMTP profile"
        message={<>Delete <strong>{deleteProfile?.name}</strong>? Profiles assigned to instances cannot be deleted.</>}
        confirmLabel="Delete profile"
        danger
      />
    </div>
  );
}

function SMTPFormModal({
  open,
  profile,
  onClose,
  onSaved,
}: {
  open: boolean;
  profile: SMTPProfile | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState(emptyForm);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setForm(
      profile
        ? {
            name: profile.name,
            host: profile.host,
            port: profile.port,
            username: profile.username,
            password: "",
            authMethod: profile.authMethod,
            tlsMode: profile.tlsMode,
            localName: profile.localName,
            fromAddress: profile.fromAddress,
            fromName: profile.fromName,
          }
        : emptyForm,
    );
  }, [open, profile]);

  function update<K extends keyof typeof form>(key: K, value: (typeof form)[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (profile) {
        await api.updateSMTPProfile(profile.id, form);
      } else {
        await api.createSMTPProfile(form);
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
      title={profile ? "Edit SMTP profile" : "New SMTP profile"}
      size="lg"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" type="submit" form="smtp-form" loading={busy}>Save profile</Button></>}
    >
      <form id="smtp-form" className="form-grid" onSubmit={(event) => void submit(event)}>
        {error ? <div className="form-error form-span">{error}</div> : null}
        <Field label="Profile name" required>
          <Input value={form.name} onChange={(e) => update("name", e.target.value)} placeholder="Transactional mail" required />
        </Field>
        <Field label="Provider preset" hint="Presets fill host and port; all fields remain editable.">
          <Select
            value=""
            onChange={(event) => {
              const presets: Record<string, { host: string; port: number; tlsMode: string }> = {
                postmark: { host: "smtp.postmarkapp.com", port: 587, tlsMode: "starttls" },
                ses: { host: "email-smtp.us-east-1.amazonaws.com", port: 587, tlsMode: "starttls" },
                mailgun: { host: "smtp.mailgun.org", port: 587, tlsMode: "starttls" },
                sendgrid: { host: "smtp.sendgrid.net", port: 587, tlsMode: "starttls" },
                resend: { host: "smtp.resend.com", port: 587, tlsMode: "starttls" },
              };
              const preset = presets[event.target.value];
              if (preset) setForm((current) => ({ ...current, ...preset }));
            }}
          >
            <option value="">Choose preset…</option>
            <option value="postmark">Postmark</option>
            <option value="ses">Amazon SES</option>
            <option value="mailgun">Mailgun</option>
            <option value="sendgrid">SendGrid</option>
            <option value="resend">Resend</option>
          </Select>
        </Field>
        <Field label="Host" required>
          <Input value={form.host} onChange={(e) => update("host", e.target.value)} placeholder="smtp.example.com" required />
        </Field>
        <Field label="Port" required>
          <Input type="number" value={form.port} onChange={(e) => update("port", Number(e.target.value))} required />
        </Field>
        <Field label="Auth method">
          <Select value={form.authMethod} onChange={(e) => update("authMethod", e.target.value)}>
            <option value="PLAIN">PLAIN</option><option value="LOGIN">LOGIN</option>
          </Select>
        </Field>
        <Field label="TLS mode" hint="Unencrypted mode is only accepted for loopback test servers and cannot be applied to PocketBase.">
          <Select value={form.tlsMode} onChange={(e) => update("tlsMode", e.target.value)}>
            <option value="starttls">STARTTLS</option><option value="tls">Implicit TLS (465)</option><option value="none">None (test only)</option>
          </Select>
        </Field>
        <Field label="Username">
          <Input value={form.username} onChange={(e) => update("username", e.target.value)} autoComplete="username" />
        </Field>
        <Field label={profile ? "Password (leave blank to keep existing)" : "Password"}>
          <Input value={form.password} onChange={(e) => update("password", e.target.value)} type="password" autoComplete="new-password" />
        </Field>
        <Field label="Sender address">
          <Input type="email" value={form.fromAddress} onChange={(e) => update("fromAddress", e.target.value)} placeholder="noreply@example.com" />
        </Field>
        <Field label="Sender name">
          <Input value={form.fromName} onChange={(e) => update("fromName", e.target.value)} placeholder="Clinic A" />
        </Field>
        <Field label="EHLO local name">
          <Input value={form.localName} onChange={(e) => update("localName", e.target.value)} placeholder="Optional" />
        </Field>
        {profile ? (
          <div className="form-span">
            <Notice tone="info">Leave the password blank to preserve the existing encrypted password. Test the profile again after changing connectivity settings.</Notice>
          </div>
        ) : null}
        <div className="form-span">
          <Badge tone="neutral"><Send size={12} /> Test after saving</Badge>
        </div>
      </form>
    </Modal>
  );
}
