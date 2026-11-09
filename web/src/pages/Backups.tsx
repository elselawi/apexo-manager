import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Archive, Download, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatBytes, formatDate, formatRelative } from "../lib/format";
import type { Backup, Instance, Job } from "../types";
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
  PageHeader,
  SearchInput,
  Select,
  StatusBadge,
  Textarea,
} from "../components/ui";
import { JobProgress } from "../components/JobProgress";

export function BackupsPage() {
  useDocumentTitle("Backups");
  const { admin } = useAuth();
  const canWrite = admin?.role !== "viewer";
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.backups(), 15000);
  const { data: instancesData } = usePolling((signal) => api.instances({ perPage: 200 }), 30000);
  const [createOpen, setCreateOpen] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const [restore, setRestore] = useState<{ backup: Backup; instance: Instance } | null>(null);
  const [deleteBackup, setDeleteBackup] = useState<Backup | null>(null);
  const [search, setSearch] = useState("");
  const [instanceFilter, setInstanceFilter] = useState("");

  const instances = instancesData?.items ?? [];
  const all = data?.items ?? [];
  const backups = all.filter((backup) => {
    if (instanceFilter && backup.instanceId !== instanceFilter) return false;
    const term = search.toLowerCase();
    return !term || (backup.instanceName ?? "").toLowerCase().includes(term) || backup.filename.toLowerCase().includes(term);
  });

  async function verify(backup: Backup) {
    try {
      const result = await api.verifyBackup(backup.id);
      setJobId(result.job.id);
      toast.success("Verification queued");
    } catch (err) {
      toast.error("Could not queue verification", errorMessage(err));
    }
  }

  async function openRestore(backup: Backup) {
    const instance = instances.find((item) => item.id === backup.instanceId);
    if (!instance) {
      toast.error("Instance not found for this backup");
      return;
    }
    setRestore({ backup, instance });
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Data protection"
        title="Backups"
        description="Verified local backup history, scheduled policies, integrity checks, and explicit restoration."
        actions={
          canWrite ? (
            <Button variant="primary" icon={<Plus size={16} />} onClick={() => setCreateOpen(true)}>Back up an instance</Button>
          ) : null
        }
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}
      {jobId ? <JobProgress jobId={jobId} dismissible onDismiss={() => setJobId(null)} onFinished={() => refresh()} /> : null}

      <div className="stats-grid">
        <div className="stat-card">
          <div className="stat-icon stat-icon-info"><Archive size={20} /></div>
          <div className="stat-copy"><span className="stat-label">Artifacts</span><strong className="stat-value">{data?.total ?? 0}</strong><span className="stat-detail">completed and verified history</span></div>
        </div>
        <div className="stat-card">
          <div className="stat-icon stat-icon-success"><ShieldCheck size={20} /></div>
          <div className="stat-copy"><span className="stat-label">Stored bytes</span><strong className="stat-value">{formatBytes(data?.totalBytes)}</strong><span className="stat-detail">local manager storage</span></div>
        </div>
        <div className="stat-card">
          <div className="stat-icon stat-icon-neutral"><RefreshCw size={20} /></div>
          <div className="stat-copy"><span className="stat-label">Latest backup</span><strong className="stat-value">{data?.items[0] ? formatRelative(data.items[0].createdAt) : "never"}</strong><span className="stat-detail">across all instances</span></div>
        </div>
      </div>

      <Card
        title="Backup history"
        subtitle="Checksums and SQLite integrity checks are recorded for every artifact."
        actions={<Button size="sm" variant="ghost" icon={<RefreshCw size={14} />} onClick={refresh}>Refresh</Button>}
        padded={false}
      >
        <div className="toolbar compact">
          <SearchInput value={search} onChange={setSearch} placeholder="Filter by instance or filename…" />
          <Select value={instanceFilter} onChange={(event) => setInstanceFilter(event.target.value)}>
            <option value="">All instances</option>
            {instances.map((instance) => <option key={instance.id} value={instance.id}>{instance.name}</option>)}
          </Select>
        </div>
        {loading && !data ? <LoadingBlock /> : backups.length === 0 ? (
          <EmptyState icon={<Archive size={26} />} title="No backups" description="Create a manual backup or configure a schedule from an instance." action={canWrite ? <Button onClick={() => setCreateOpen(true)}>Create backup</Button> : undefined} />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead><tr><th>Created</th><th>Instance</th><th>Method</th><th>Status</th><th>Size</th><th>Kind</th><th>Verified</th><th /></tr></thead>
              <tbody>
                {backups.map((backup) => (
                  <tr key={backup.id}>
                    <td>
                      <strong>{formatDate(backup.createdAt)}</strong>
                      <small className="muted mono">{backup.filename}</small>
                    </td>
                    <td><Link to={`/instances/${backup.instanceId}`} className="text-link">{backup.instanceName}</Link></td>
                    <td><Badge tone={backup.method === "pocketbase_native" ? "success" : "info"}>{backup.method === "pocketbase_native" ? "PocketBase native" : "SQLite snapshot"}</Badge></td>
                    <td><StatusBadge status={backup.status} />{backup.error ? <small className="text-danger">{backup.error}</small> : null}</td>
                    <td>{formatBytes(backup.size)}</td>
                    <td className="muted">{backup.automatic ? "scheduled" : "manual"}</td>
                    <td className="muted">{backup.verifiedAt ? formatRelative(backup.verifiedAt) : "—"}</td>
                    <td className="table-actions">
                      <Button size="sm" variant="ghost" icon={<Download size={14} />} onClick={() => void api.downloadBackup(backup.id).catch((err) => toast.error("Download failed", errorMessage(err)))}>Download</Button>
                      {canWrite ? <Button size="sm" variant="ghost" onClick={() => void verify(backup)}>Verify</Button> : null}
                      {canWrite ? <Button size="sm" variant="ghost" onClick={() => void openRestore(backup)} disabled={!["completed", "verified"].includes(backup.status)}>Restore</Button> : null}
                      {admin?.role === "owner" ? <IconButton label="Delete backup" onClick={() => setDeleteBackup(backup)}><Trash2 size={15} /></IconButton> : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="Backup methods" subtitle="Native when possible; safe infrastructure snapshot otherwise.">
        <div className="method-grid">
          <div className="method-card">
            <Badge tone="success">PocketBase native</Badge>
            <p>Uses the official backup API with a linked superuser. PocketBase creates a consistent archive including its data directory; the manager downloads it into protected storage and verifies its database.</p>
          </div>
          <div className="method-card">
            <Badge tone="info">SQLite snapshot</Badge>
            <p>Uses SQLite <code>VACUUM INTO</code> for a consistent database snapshot, then archives uploads and relevant service/Caddy configuration. Used when application authentication is unavailable.</p>
          </div>
        </div>
        <div className="button-row">
          <Link to="/instances" className="button button-secondary button-md">Configure instance schedules</Link>
        </div>
      </Card>

      <CreateBackupModal
        open={createOpen}
        instances={instances}
        onClose={() => setCreateOpen(false)}
        onQueued={(job) => { setCreateOpen(false); setJobId(job.id); toast.success("Backup queued"); }}
      />
      <RestoreModal
        selection={restore}
        onClose={() => setRestore(null)}
        onQueued={(job) => { setRestore(null); setJobId(job.id); toast.warning("Restore queued", "A safety backup will be created first."); }}
      />
      <ConfirmDialog
        open={Boolean(deleteBackup)}
        onClose={() => setDeleteBackup(null)}
        onConfirm={() => {
          if (!deleteBackup) return;
          void api.deleteBackup(deleteBackup.id)
            .then(() => { toast.success("Backup deleted"); setDeleteBackup(null); refresh(); })
            .catch((err) => toast.error("Could not delete backup", errorMessage(err)));
        }}
        title="Delete backup artifact"
        message={<>Delete <strong>{deleteBackup?.filename}</strong> from disk? This cannot be undone.</>}
        confirmLabel="Delete backup"
        danger
        requireText={deleteBackup?.filename}
      />
    </div>
  );
}

function CreateBackupModal({
  open,
  instances,
  onClose,
  onQueued,
}: {
  open: boolean;
  instances: Instance[];
  onClose: () => void;
  onQueued: (job: Job) => void;
}) {
  const [instanceId, setInstanceId] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setInstanceId(instances[0]?.id ?? "");
      setNote("");
      setError(null);
    }
  }, [open, instances]);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const result = await api.createBackup(instanceId, note);
      onQueued(result.job);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Create backup" description="Runs as a persistent job; closing the browser does not interrupt it." footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" onClick={() => void submit()} loading={busy} disabled={!instanceId}>Create backup</Button></>}>
      <div className="stack">
        {error ? <div className="form-error">{error}</div> : null}
        <Field label="Instance" required>
          <Select value={instanceId} onChange={(event) => setInstanceId(event.target.value)}>
            <option value="">Select instance</option>
            {instances.map((instance) => <option key={instance.id} value={instance.id}>{instance.name} · {instance.status}</option>)}
          </Select>
        </Field>
        <Field label="Note">
          <Textarea value={note} onChange={(event) => setNote(event.target.value)} rows={3} placeholder="Optional context for this backup" />
        </Field>
      </div>
    </Modal>
  );
}

function RestoreModal({
  selection,
  onClose,
  onQueued,
}: {
  selection: { backup: Backup; instance: Instance } | null;
  onClose: () => void;
  onQueued: (job: Job) => void;
}) {
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (selection) {
      setConfirm("");
      setError(null);
    }
  }, [selection]);

  async function submit() {
    if (!selection) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.restoreBackup(selection.backup.id, confirm);
      onQueued(result.job);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={Boolean(selection)}
      onClose={onClose}
      title="Restore backup"
      size="sm"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="danger" onClick={() => void submit()} loading={busy} disabled={confirm !== selection?.instance.name}>Restore</Button></>}
    >
      {selection ? (
        <div className="stack">
          {error ? <div className="form-error">{error}</div> : null}
          <p>Restore <strong>{selection.backup.filename}</strong> to <strong>{selection.instance.name}</strong>?</p>
          <p className="muted">The manager verifies the artifact, creates a new safety backup, stops the service, restores data, starts the service, and verifies health. The previous data directory is retained.</p>
          <Field label={`Type ${selection.instance.name} to confirm`}>
            <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoFocus />
          </Field>
        </div>
      ) : null}
    </Modal>
  );
}
