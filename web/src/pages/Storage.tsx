import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { Database, HardDrive, Plus, Trash2, Wifi } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatRelative } from "../lib/format";
import type { ApplicationConfig, StorageProfile } from "../types";
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
  provider: "digitalocean",
  endpoint: "",
  region: "",
  bucket: "",
  accessKey: "",
  secretKey: "",
  forcePathStyle: true,
};

const providerHelp: Record<string, { endpoint: string; region: string }> = {
  digitalocean: { endpoint: "https://nyc3.digitaloceanspaces.com", region: "nyc3" },
  r2: { endpoint: "https://<account-id>.r2.cloudflarestorage.com", region: "auto" },
  aws: { endpoint: "Blank for AWS default endpoint", region: "us-east-1" },
  custom: { endpoint: "https://s3.example.com", region: "us-east-1" },
};

export function StoragePage() {
  useDocumentTitle("S3 Storage");
  const { admin } = useAuth();
  const canWrite = admin?.role !== "viewer";
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.storageProfiles(), 20000);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<StorageProfile | null>(null);
  const [deleteProfile, setDeleteProfile] = useState<StorageProfile | null>(null);
  const [testing, setTesting] = useState<string | null>(null);
  const [draft, setDraft] = useState<ApplicationConfig["s3"] | null>(null);
  const location = useLocation();
  const navigate = useNavigate();

  const imported = (location.state as { imported?: ApplicationConfig["s3"] } | null)?.imported;
  useEffect(() => {
    if (!imported) return;
    setEditing(null);
    setDraft(imported);
    setModalOpen(true);
    navigate(location.pathname, { replace: true, state: null });
  }, [imported, location.pathname, navigate]);

  const profiles = data?.items ?? [];

  async function test(profile: StorageProfile) {
    setTesting(profile.id);
    try {
      const result = await api.testStorageProfile(profile.id);
      if (result.ok) toast.success("Bucket access verified", `${profile.bucket} @ ${profile.region}`);
      else toast.error("Storage test failed", result.error);
      refresh();
    } catch (err) {
      toast.error("Storage test failed", errorMessage(err));
    } finally {
      setTesting(null);
    }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Application settings"
        title="S3-compatible storage"
        description="Reusable DigitalOcean Spaces, Cloudflare R2, AWS S3, and custom S3 profiles with independent credential tests."
        actions={canWrite ? <Button variant="primary" icon={<Plus size={16} />} onClick={() => { setEditing(null); setModalOpen(true); }}>New storage profile</Button> : null}
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}
      <Notice tone="info" title="Profiles are optional deployment templates">
        Existing instance S3 settings are visible under the instance&apos;s <strong>Application settings</strong>.
        You do not need a profile to keep them. A profile is used only when you want pbmanager to apply or change the
        configuration; imported values leave the secret key blank because PocketBase never returns it.
      </Notice>
      <Notice tone="info" title="Validation and application are separate">
        Credentials are validated with a signed ListObjectsV2 request. Applying to PocketBase requires a linked
        superuser and uses the documented settings API. Secrets are encrypted at rest and never returned.
      </Notice>

      {loading && !data ? <LoadingBlock /> : profiles.length === 0 ? (
        <Card>
          <EmptyState
            icon={<HardDrive size={28} />}
            title="No storage profiles"
            description="Add credentials for a bucket, validate access, then assign and apply the profile to instances."
            action={canWrite ? <Button variant="primary" onClick={() => setModalOpen(true)}>Create profile</Button> : undefined}
          />
        </Card>
      ) : (
        <div className="profile-grid">
          {profiles.map((profile) => (
            <Card
              key={profile.id}
              title={<span className="profile-title"><span className="profile-icon"><HardDrive size={17} /></span>{profile.name}</span>}
              subtitle={`${profile.bucket} · ${profile.region}`}
              actions={<StatusBadge status={profile.status} />}
            >
              <div className="profile-meta-grid">
                <div><span>Provider</span><strong>{profile.provider === "r2" ? "Cloudflare R2" : profile.provider === "digitalocean" ? "DO Spaces" : profile.provider.toUpperCase()}</strong></div>
                <div><span>Path style</span><strong>{profile.forcePathStyle ? "enabled" : "virtual host"}</strong></div>
                <div><span>Access key</span><strong className="mono">{profile.accessKey.slice(0, 6)}…</strong></div>
                <div><span>Assigned to</span><strong>{profile.usageCount ?? 0} instances</strong></div>
              </div>
              <div className="endpoint-box mono">{profile.endpoint || "AWS default endpoint"}</div>
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

      <Card title="Storage pipeline" subtitle="Distinct states prevent false success reporting.">
        <div className="pipeline-stages">
          <div><span>1</span><strong>Profile created</strong><p>Secret encrypted at rest and redacted from every response.</p></div>
          <div><span>2</span><strong>Credentials validated</strong><p>Signed S3 request confirms endpoint, bucket, and key permissions.</p></div>
          <div><span>3</span><strong>PocketBase applied</strong><p>Partial settings PATCH through the public API.</p></div>
          <div><span>4</span><strong>Read-back verified</strong><p>Bucket, region, and enabled state are checked again.</p></div>
        </div>
        <div className="button-row">
          <Link to="/instances" className="button button-secondary button-md"><Database size={15} /> Assign & apply on an instance</Link>
        </div>
      </Card>

      <StorageFormModal
        open={modalOpen}
        profile={editing}
        draft={draft}
        onClose={() => { setModalOpen(false); setDraft(null); }}
        onSaved={() => { refresh(); toast.success("Storage profile saved"); setDraft(null); }}
      />
      <ConfirmDialog
        open={Boolean(deleteProfile)}
        onClose={() => setDeleteProfile(null)}
        onConfirm={() => {
          if (!deleteProfile) return;
          void api.deleteStorageProfile(deleteProfile.id)
            .then(() => { toast.success("Storage profile deleted"); setDeleteProfile(null); refresh(); })
            .catch((err) => toast.error("Could not delete profile", errorMessage(err)));
        }}
        title="Delete storage profile"
        message={<>Delete <strong>{deleteProfile?.name}</strong>? Profiles assigned to instances cannot be deleted.</>}
        confirmLabel="Delete profile"
        danger
      />
    </div>
  );
}

function StorageFormModal({
  open,
  profile,
  draft,
  onClose,
  onSaved,
}: {
  open: boolean;
  profile: StorageProfile | null;
  draft?: ApplicationConfig["s3"] | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState(emptyForm);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    if (draft) {
      setForm({
        name: `Imported ${draft.bucket || draft.endpoint || "S3"}`,
        provider: draft.endpoint ? "custom" : "aws",
        endpoint: draft.endpoint,
        region: draft.region,
        bucket: draft.bucket,
        accessKey: draft.accessKey,
        secretKey: "",
        forcePathStyle: draft.forcePathStyle,
      });
      return;
    }
    setForm(
      profile
        ? {
            name: profile.name,
            provider: profile.provider,
            endpoint: profile.endpoint,
            region: profile.region,
            bucket: profile.bucket,
            accessKey: profile.accessKey,
            secretKey: "",
            forcePathStyle: profile.forcePathStyle,
          }
        : emptyForm,
    );
  }, [open, profile, draft]);

  function update<K extends keyof typeof form>(key: K, value: (typeof form)[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (profile) await api.updateStorageProfile(profile.id, form);
      else await api.createStorageProfile(form);
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const help = providerHelp[form.provider] ?? providerHelp.custom;

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={profile ? "Edit storage profile" : draft ? "Import current S3 settings" : "New storage profile"}
      size="lg"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" type="submit" form="storage-form" loading={busy}>Save profile</Button></>}
    >
      <form id="storage-form" className="form-grid" onSubmit={(event) => void submit(event)}>
        {error ? <div className="form-error form-span">{error}</div> : null}
        {draft ? (
          <div className="form-span">
            <Notice tone="info">
              Non-secret S3 values were read from PocketBase. The secret access key is not returned, so re-enter it
              before saving; applying the profile later is an explicit instance action.
            </Notice>
          </div>
        ) : null}
        <Field label="Profile name" required>
          <Input value={form.name} onChange={(e) => update("name", e.target.value)} required />
        </Field>
        <Field label="Provider">
          <Select value={form.provider} onChange={(e) => update("provider", e.target.value)}>
            <option value="digitalocean">DigitalOcean Spaces</option>
            <option value="r2">Cloudflare R2</option>
            <option value="aws">Amazon S3</option>
            <option value="custom">Custom S3-compatible</option>
          </Select>
        </Field>
        <Field label="Endpoint" hint={help.endpoint} required={form.provider !== "aws"}>
          <Input value={form.endpoint} onChange={(e) => update("endpoint", e.target.value)} className="mono" placeholder={help.endpoint} required={form.provider !== "aws"} />
        </Field>
        <Field label="Region" required>
          <Input value={form.region} onChange={(e) => update("region", e.target.value)} placeholder={help.region} required />
        </Field>
        <Field label="Bucket" required>
          <Input value={form.bucket} onChange={(e) => update("bucket", e.target.value)} required />
        </Field>
        <Field label="Access key" required>
          <Input value={form.accessKey} onChange={(e) => update("accessKey", e.target.value)} className="mono" required />
        </Field>
        <Field label={profile ? "Secret key (leave blank to keep existing)" : "Secret key"} required={!profile}>
          <Input type="password" value={form.secretKey} onChange={(e) => update("secretKey", e.target.value)} autoComplete="new-password" required={!profile} />
        </Field>
        <div className="switch-field">
          <Switch checked={form.forcePathStyle} onChange={(value) => update("forcePathStyle", value)} label="Force path-style addressing" description="Usually required for Spaces, R2, and custom endpoints." />
        </div>
        {profile ? <div className="form-span"><Notice tone="info">Leave the secret blank to preserve the existing encrypted secret.</Notice></div> : null}
        <div className="form-span"><Badge tone="neutral">Bucket access is tested separately after saving</Badge></div>
      </form>
    </Modal>
  );
}
