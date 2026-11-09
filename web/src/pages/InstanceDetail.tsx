import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  Activity,
  Archive,
  ArrowLeft,
  ArrowUpRight,
  Boxes,
  CheckCircle2,
  Cloud,
  Database,
  Download,
  ExternalLink,
  FileClock,
  Globe2,
  HardDrive,
  KeyRound,
  Network,
  Play,
  Power,
  RefreshCw,
  RotateCcw,
  Save,
  ServerCog,
  ShieldAlert,
  Square,
  Terminal,
  Trash2,
  Unplug,
  UploadCloud,
  Wrench,
} from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import {
  formatBytes,
  formatDate,
  formatRelative,
  jobTypeLabel,
  percent,
  statusTone,
  titleCase,
} from "../lib/format";
import type { Backup, Domain, Job, SMTPProfile, StorageProfile } from "../types";
import {
  Badge,
  Button,
  Card,
  CodeBlock,
  ConfirmDialog,
  CopyField,
  EmptyState,
  ErrorBanner,
  Field,
  IconButton,
  Input,
  KeyValue,
  LoadingBlock,
  Modal,
  Notice,
  PageHeader,
  ProgressBar,
  Select,
  StatusBadge,
  Switch,
  Tabs,
  Textarea,
} from "../components/ui";
import { JobProgress } from "../components/JobProgress";
import { DomainAttachModal } from "../components/DomainAttachModal";

type TabId = "overview" | "application" | "domains" | "backups" | "logs" | "danger";

export function InstanceDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const toast = useToast();
  const { admin } = useAuth();
  const canWrite = admin?.role === "admin" || admin?.role === "owner";
  useDocumentTitle("Instance detail");

  const { data, error, loading, refresh } = usePolling((signal) => api.instance(id), 10000, [id]);
  const [tab, setTab] = useState<TabId>("overview");
  const [jobId, setJobId] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [updateOpen, setUpdateOpen] = useState(false);
  const [credentialsOpen, setCredentialsOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [domainOpen, setDomainOpen] = useState(false);
  const [detachDomain, setDetachDomain] = useState<Domain | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [restoreBackup, setRestoreBackup] = useState<Backup | null>(null);
  const [logs, setLogs] = useState("");
  const [logsLoading, setLogsLoading] = useState(false);
  const [logLines, setLogLines] = useState(200);

  useEffect(() => {
    if (tab !== "logs" || !data || logs) return;
    void loadLogs();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, data?.instance.id]);

  if (loading && !data) return <LoadingBlock label="Loading instance…" />;
  if (!data) return <ErrorBanner message={error ?? "Instance not found"} onRetry={refresh} />;

  const { instance, domains, backups, jobs, url } = data;
  const activeJob = jobId ?? instance.openJobId ?? null;

  async function runAction(action: "start" | "stop" | "restart" | "sync-caddy" | "apply-smtp" | "apply-storage") {
    setBusyAction(action);
    try {
      const result = await api.instanceAction(instance.id, action);
      setJobId(result.job.id);
      toast.success("Operation queued", jobTypeLabel(result.job.type));
    } catch (err) {
      toast.error("Operation failed to queue", errorMessage(err));
    } finally {
      setBusyAction(null);
    }
  }

  async function verify() {
    setBusyAction("verify");
    try {
      const result = await api.verifyInstance(instance.id);
      toast.success("Verification complete", `${String(result.status)} / ${String(result.health)}`);
      refresh();
    } catch (err) {
      toast.error("Verification failed", errorMessage(err));
    } finally {
      setBusyAction(null);
    }
  }

  async function loadLogs() {
    setLogsLoading(true);
    try {
      const result = await api.instanceLogs(instance.id, logLines);
      setLogs(result.logs || "No journal entries returned.");
    } catch (err) {
      setLogs(`Could not load logs:\n${errorMessage(err)}`);
    } finally {
      setLogsLoading(false);
    }
  }

  async function downloadBackup(backup: Backup) {
    try {
      await api.downloadBackup(backup.id);
      toast.success("Backup download started");
    } catch (err) {
      toast.error("Download failed", errorMessage(err));
    }
  }

  async function verifyBackup(backup: Backup) {
    try {
      const result = await api.verifyBackup(backup.id);
      setJobId(result.job.id);
      toast.success("Verification queued");
    } catch (err) {
      toast.error("Could not queue verification", errorMessage(err));
    }
  }

  const primaryDomain = domains[0];
  const tabs: { id: TabId; label: string; count?: number }[] = [
    { id: "overview", label: "Overview" },
    { id: "application", label: "Application settings" },
    { id: "domains", label: "Domains", count: domains.length },
    { id: "backups", label: "Backups", count: backups.length },
    { id: "logs", label: "Logs" },
    ...(canWrite ? [{ id: "danger" as TabId, label: "Danger zone" }] : []),
  ];

  return (
    <div className="page">
      <div className="breadcrumb">
        <Link to="/instances"><ArrowLeft size={14} /> Instances</Link>
        <span>/</span>
        <span>{instance.name}</span>
      </div>
      <PageHeader
        eyebrow={`${instance.source} · ${instance.managed ? "managed" : "imported inventory"}`}
        title={instance.name}
        description={
          <span className="instance-headline-meta">
            <StatusBadge status={instance.status} />
            <StatusBadge status={instance.health} label={instance.health === "healthy" ? "Health OK" : titleCase(instance.health)} />
            <span className="mono">127.0.0.1:{instance.port}</span>
            {instance.pbVersion ? <Badge tone="neutral">PocketBase {instance.pbVersion}</Badge> : null}
            <a href={url} target="_blank" rel="noreferrer" className="text-link">
              Open instance <ExternalLink size={13} />
            </a>
          </span>
        }
        actions={
          canWrite ? (
            <>
              <Button size="sm" variant="secondary" icon={<Play size={15} />} onClick={() => void runAction("start")} disabled={busyAction !== null} loading={busyAction === "start"}>
                Start
              </Button>
              <Button size="sm" variant="secondary" icon={<Square size={14} />} onClick={() => void runAction("stop")} disabled={busyAction !== null} loading={busyAction === "stop"}>
                Stop
              </Button>
              <Button size="sm" variant="secondary" icon={<RotateCcw size={15} />} onClick={() => void runAction("restart")} disabled={busyAction !== null} loading={busyAction === "restart"}>
                Restart
              </Button>
              <Button size="sm" variant="secondary" icon={<Wrench size={15} />} onClick={() => void verify()} loading={busyAction === "verify"}>
                Verify
              </Button>
              <Button size="sm" variant="primary" icon={<UploadCloud size={15} />} onClick={() => setUpdateOpen(true)}>
                Update
              </Button>
            </>
          ) : null
        }
      />

      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}
      {instance.lastHealthError ? <Notice tone="warning" title="Latest health error">{instance.lastHealthError}</Notice> : null}
      {activeJob ? (
        <JobProgress
          jobId={activeJob}
          dismissible
          onDismiss={() => setJobId(null)}
          onFinished={(job) => {
            refresh();
            if (job.status === "succeeded") toast.success("Operation completed", jobTypeLabel(job.type));
            else if (job.status === "failed") toast.error("Operation failed", job.lastError);
          }}
        />
      ) : null}

      <Tabs tabs={tabs} active={tab} onChange={setTab} />

      {tab === "overview" ? (
        <OverviewTab data={data} onSchedule={() => setScheduleOpen(true)} onRunAction={runAction} />
      ) : null}
      {tab === "application" ? (
        <ApplicationTab
          data={data}
          canWrite={canWrite}
          onRefresh={refresh}
          onJob={(job) => setJobId(job.id)}
          onCredentials={() => setCredentialsOpen(true)}
        />
      ) : null}
      {tab === "domains" ? (
        <Card
          title="Domains"
          subtitle="DNS, Caddy, and TLS state for hostnames mapped to this instance."
          actions={canWrite ? <Button size="sm" variant="primary" icon={<Globe2 size={15} />} onClick={() => setDomainOpen(true)}>Attach domain</Button> : null}
          padded={false}
        >
          {domains.length === 0 ? (
            <EmptyState icon={<Globe2 size={24} />} title="No domains mapped" description="Attach a hostname to manage its Caddy route and Cloudflare DNS record." />
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr><th>Hostname</th><th>DNS</th><th>Caddy</th><th>TLS</th><th>Managed</th><th>Checked</th><th /></tr>
                </thead>
                <tbody>
                  {domains.map((domain) => (
                    <tr key={domain.id}>
                      <td><strong>{domain.hostname}</strong>{domain.proxied ? <Badge tone="warning">Proxied</Badge> : null}</td>
                      <td><StatusBadge status={domain.dnsStatus} /></td>
                      <td><StatusBadge status={domain.caddyStatus} /></td>
                      <td><StatusBadge status={domain.tlsStatus} /></td>
                      <td className="small muted">{domain.dnsManaged ? "DNS " : ""}{domain.caddyManaged ? "Caddy" : ""}{!domain.dnsManaged && !domain.caddyManaged ? "inventory" : ""}</td>
                      <td className="muted small">{formatRelative(domain.lastCheckedAt)}</td>
                      <td className="table-action">
                        <Button size="sm" variant="ghost" onClick={() => void api.checkDomain(domain.id).then(() => { toast.success("Domain checked"); refresh(); }).catch((err) => toast.error("Check failed", errorMessage(err)))}>
                          Check
                        </Button>
                        {canWrite ? <IconButton label="Remove domain" onClick={() => setDetachDomain(domain)}><Trash2 size={15} /></IconButton> : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      ) : null}
      {tab === "backups" ? (
        <BackupsTab
          backups={backups}
          instance={instance}
          canWrite={canWrite}
          onRefresh={refresh}
          onJob={(job) => setJobId(job.id)}
          onDownload={(backup) => void downloadBackup(backup)}
          onVerify={(backup) => void verifyBackup(backup)}
          onRestore={setRestoreBackup}
          onSchedule={() => setScheduleOpen(true)}
        />
      ) : null}
      {tab === "logs" ? (
        <Card
          title="Service logs"
          subtitle={`journalctl -u ${instance.serviceName}`}
          actions={
            <div className="button-row">
              <Select value={logLines} onChange={(event) => setLogLines(Number(event.target.value))} className="compact-select">
                <option value={100}>100 lines</option><option value={200}>200 lines</option><option value={500}>500 lines</option><option value={1000}>1000 lines</option>
              </Select>
              <Button size="sm" variant="secondary" icon={<RefreshCw size={14} />} onClick={() => void loadLogs()} loading={logsLoading}>Refresh</Button>
            </div>
          }
        >
          {logsLoading && !logs ? <LoadingBlock label="Reading journal…" /> : <CodeBlock maxHeight={620}>{logs || "No logs loaded."}</CodeBlock>}
        </Card>
      ) : null}
      {tab === "danger" && canWrite ? (
        <DangerTab instance={instance} onDelete={() => setDeleteOpen(true)} />
      ) : null}

      <UpdateModal
        open={updateOpen}
        instanceId={instance.id}
        currentVersion={instance.pbVersion}
        onClose={() => setUpdateOpen(false)}
        onQueued={(job) => { setUpdateOpen(false); setJobId(job.id); }}
      />
      <CredentialsModal
        open={credentialsOpen}
        instanceId={instance.id}
        email={instance.pbSuperuserEmail ?? ""}
        onClose={() => setCredentialsOpen(false)}
        onSaved={refresh}
      />
      <ScheduleModal
        open={scheduleOpen}
        instance={instance}
        onClose={() => setScheduleOpen(false)}
        onSaved={refresh}
      />
      <DomainAttachModal
        open={domainOpen}
        instanceId={instance.id}
        instanceName={instance.name}
        onClose={() => setDomainOpen(false)}
        onCreated={(domain) => { refresh(); if (domain.dnsStatus === "pending" || domain.caddyStatus === "pending") toast.info("Domain attached", "Integration job queued."); }}
      />
      <DetachDomainModal
        domain={detachDomain}
        onClose={() => setDetachDomain(null)}
        onQueued={(job) => { setDetachDomain(null); setJobId(job.id); }}
      />
      <RestoreBackupModal
        backup={restoreBackup}
        instanceName={instance.name}
        onClose={() => setRestoreBackup(null)}
        onQueued={(job) => { setRestoreBackup(null); setJobId(job.id); }}
      />
      <DeleteInstanceModal
        open={deleteOpen}
        instance={instance}
        domains={domains}
        onClose={() => setDeleteOpen(false)}
        onQueued={(job) => { setDeleteOpen(false); toast.warning("Deletion queued", job.id); navigate("/instances"); }}
      />
    </div>
  );
}

function OverviewTab({
  data,
  onSchedule,
  onRunAction,
}: {
  data: import("../types").InstanceDetail;
  onSchedule: () => void;
  onRunAction: (action: "start" | "stop" | "restart" | "sync-caddy" | "apply-smtp" | "apply-storage") => Promise<void>;
}) {
  const { instance, currentMetrics, backups, domains, jobs } = data;
  return (
    <div className="detail-grid">
      <div className="detail-main">
        <div className="mini-stat-grid">
          <div className="mini-stat"><ServerCog size={18} /><span>Service</span><strong>{instance.status}</strong><small>{instance.serviceName || "none"}</small></div>
          <div className="mini-stat"><Activity size={18} /><span>Health</span><strong>{instance.health}</strong><small>{(currentMetrics?.portListening ?? instance.portListening) ? "port listening" : "port closed"}</small></div>
          <div className="mini-stat"><HardDrive size={18} /><span>Memory</span><strong>{formatBytes(currentMetrics?.memBytes ?? instance.memoryBytes)}</strong><small>cgroup usage</small></div>
          <div className="mini-stat"><Database size={18} /><span>Disk</span><strong>{formatBytes(currentMetrics?.diskBytes ?? instance.diskBytes)}</strong><small>data directory</small></div>
        </div>

        <Card title="Installation" subtitle="Inventory paths and systemd configuration">
          <KeyValue
            items={[
              { label: "Base directory", value: instance.baseDir, mono: true },
              { label: "Data directory", value: instance.dataDir, mono: true },
              { label: "Binary", value: instance.binaryPath, mono: true },
              { label: "Service file", value: instance.serviceFile || "—", mono: true },
              { label: "Run-as user", value: instance.runAsUser || "root", mono: true },
              { label: "PocketBase version", value: instance.pbVersion || "unknown" },
              { label: "Source", value: titleCase(instance.source) },
              { label: "Last seen", value: formatRelative(instance.lastSeenAt) },
            ]}
          />
          {instance.notes ? <div className="notes-block">{instance.notes}</div> : null}
        </Card>

        <Card
          title="Backup schedule"
          subtitle={instance.backupEnabled ? `${instance.backupCron} UTC · retention ${instance.backupRetention}` : "Scheduled backups are disabled"}
          actions={<Button size="sm" variant="secondary" icon={<Archive size={15} />} onClick={onSchedule}>Edit schedule</Button>}
        >
          <div className="backup-schedule-row">
            <div><span className="muted">Next backup</span><strong>{instance.nextBackupAt ? formatDate(instance.nextBackupAt) : "not scheduled"}</strong></div>
            <div><span className="muted">Last successful</span><strong>{instance.lastBackupAt ? formatDate(instance.lastBackupAt) : "never"}</strong></div>
            <div><span className="muted">Recent artifacts</span><strong>{backups.length}</strong></div>
          </div>
        </Card>

        <Card title="Recent operations" subtitle="Persistent jobs related to this instance" padded={false}>
          {jobs.length === 0 ? <EmptyState compact icon={<FileClock size={22} />} title="No active jobs" /> : (
            <div className="timeline">
              {jobs.map((job) => (
                <Link to={`/jobs?job=${job.id}`} className="timeline-row" key={job.id}>
                  <span className={`timeline-dot timeline-${job.status}`} />
                  <div><strong>{jobTypeLabel(job.type)}</strong><span>{formatDate(job.createdAt)}</span></div>
                  <StatusBadge status={job.status} />
                </Link>
              ))}
            </div>
          )}
          <div className="card-footer-link"><Link to="/jobs" className="text-link">Open job queue <ArrowUpRight size={13} /></Link></div>
        </Card>
      </div>

      <aside className="detail-side">
        <Card title="Domains">
          {domains.length === 0 ? <p className="muted">No hostnames mapped.</p> : (
            <div className="domain-mini-list">
              {domains.map((domain) => (
                <div key={domain.id}>
                  <Globe2 size={15} />
                  <div><strong>{domain.hostname}</strong><span>DNS {domain.dnsStatus} · TLS {domain.tlsStatus}</span></div>
                </div>
              ))}
            </div>
          )}
        </Card>
        <Card title="PocketBase API" subtitle="Application-level management">
          <div className="connection-state">
            <KeyRound size={20} />
            <div><StatusBadge status={instance.pbAuthStatus} /><span>{instance.pbSuperuserEmail || "No superuser linked"}</span></div>
          </div>
          <p className="muted small">
            Infrastructure operations work without PocketBase authentication. Linking a dedicated superuser enables
            supported settings APIs and native backups.
          </p>
        </Card>
        <Card title="Infrastructure actions">
          <div className="vertical-actions">
            <Button size="sm" variant="secondary" icon={<Network size={15} />} onClick={() => void onRunAction("sync-caddy")}>Sync Caddy routes</Button>
            <Button size="sm" variant="secondary" icon={<RefreshCw size={15} />} onClick={() => (window.location.href = `/instances/${instance.id}`)}>Refresh data</Button>
            <Link to="/monitoring" className="button button-ghost button-sm"><Activity size={15} /><span>Open monitoring</span></Link>
          </div>
        </Card>
      </aside>
    </div>
  );
}

function ApplicationTab({
  data,
  canWrite,
  onRefresh,
  onJob,
  onCredentials,
}: {
  data: import("../types").InstanceDetail;
  canWrite: boolean;
  onRefresh: () => void;
  onJob: (job: Job) => void;
  onCredentials: () => void;
}) {
  const { instance } = data;
  const toast = useToast();
  const [smtpProfiles, setSmtpProfiles] = useState<SMTPProfile[]>([]);
  const [storageProfiles, setStorageProfiles] = useState<StorageProfile[]>([]);
  const [smtpId, setSmtpId] = useState(instance.smtpProfileId ?? "");
  const [storageId, setStorageId] = useState(instance.storageProfileId ?? "");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  useEffect(() => {
    void Promise.all([
      api.smtpProfiles().then((result) => setSmtpProfiles(result.items)).catch(() => undefined),
      api.storageProfiles().then((result) => setStorageProfiles(result.items)).catch(() => undefined),
    ]);
  }, []);
  useEffect(() => {
    setSmtpId(instance.smtpProfileId ?? "");
    setStorageId(instance.storageProfileId ?? "");
  }, [instance.smtpProfileId, instance.storageProfileId]);

  async function saveAssignments() {
    setSaving(true);
    try {
      await api.updateInstanceSettings(instance.id, { smtpProfileId: smtpId, storageProfileId: storageId });
      toast.success("Assignments saved", "Use Apply to push settings through PocketBase's supported API.");
      onRefresh();
    } catch (err) {
      toast.error("Could not save assignments", errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function apply(kind: "smtp" | "storage") {
    try {
      const result = await api.instanceAction(instance.id, kind === "smtp" ? "apply-smtp" : "apply-storage");
      onJob(result.job);
      toast.success("Apply job queued");
    } catch (err) {
      toast.error("Could not queue apply job", errorMessage(err));
    }
  }

  async function testLinked() {
    setTesting(true);
    try {
      const result = await api.testCredentials(instance.id);
      if (result.ok) toast.success("PocketBase authentication works");
      else toast.error("Authentication failed", result.error);
    } catch (err) {
      toast.error("Test failed", errorMessage(err));
    } finally {
      setTesting(false);
    }
  }

  async function unlink() {
    try {
      await api.clearCredentials(instance.id);
      toast.success("PocketBase credentials unlinked");
      onRefresh();
    } catch (err) {
      toast.error("Could not unlink", errorMessage(err));
    }
  }

  return (
    <div className="detail-grid">
      <div className="detail-main">
        <Card
          title="PocketBase management credentials"
          subtitle="Optional, encrypted, and scoped to this instance"
          actions={
            canWrite ? (
              <div className="button-row">
                {instance.pbAuthStatus === "linked" ? (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => void testLinked()} loading={testing}>Test</Button>
                    <Button size="sm" variant="ghost" icon={<Unplug size={14} />} onClick={() => void unlink()}>Unlink</Button>
                  </>
                ) : null}
                <Button size="sm" variant="secondary" icon={<KeyRound size={15} />} onClick={onCredentials}>
                  {instance.pbAuthStatus === "linked" ? "Replace credentials" : "Link superuser"}
                </Button>
              </div>
            ) : null
          }
        >
          <div className="connection-panel">
            <div className={`connection-icon ${instance.pbAuthStatus === "linked" ? "connected" : ""}`}><KeyRound size={22} /></div>
            <div>
              <StatusBadge status={instance.pbAuthStatus} />
              <strong>{instance.pbSuperuserEmail || "No superuser linked"}</strong>
              <p>
                {instance.pbAuthStatus === "linked"
                  ? "SMTP/S3 settings and native PocketBase backups use this account through documented APIs."
                  : "Without a linked superuser, SMTP/S3 application settings and native backups are unavailable. Infrastructure management still works."}
              </p>
              {instance.pbAuthError ? <div className="form-error">{instance.pbAuthError}</div> : null}
            </div>
          </div>
          <Notice tone="info">
            The manager never bypasses PocketBase authentication and never writes to internal PocketBase tables. Use a
            dedicated superuser for this integration.
          </Notice>
        </Card>

        <Card title="SMTP profile" subtitle="Assignment is not application. Apply pushes settings via PocketBase's settings API.">
          <div className="form-grid">
            <Field label="Assigned profile">
              <Select value={smtpId} onChange={(event) => setSmtpId(event.target.value)}>
                <option value="">No profile</option>
                {smtpProfiles.map((profile) => <option value={profile.id} key={profile.id}>{profile.name} · {profile.host}:{profile.port}</option>)}
              </Select>
            </Field>
            <div className="field">
              <span className="field-label">Pipeline status</span>
              <div className="pipeline">
                <Badge tone={statusTone(smtpProfiles.find((p) => p.id === smtpId)?.status)}>{smtpProfiles.find((p) => p.id === smtpId)?.status ?? "profile not selected"}</Badge>
                <span>→</span>
                <Badge tone={statusTone(instance.smtpApplyStatus)}>{instance.smtpApplyStatus}</Badge>
              </div>
              <div className="field-hint">
                Profile created → credentials tested → applied → operational. Operational is never claimed without verification.
              </div>
            </div>
          </div>
          {canWrite ? (
            <div className="button-row">
              <Button variant="secondary" onClick={() => void saveAssignments()} loading={saving} icon={<Save size={15} />}>Save assignments</Button>
              <Button variant="primary" onClick={() => void apply("smtp")} disabled={!instance.smtpProfileId} icon={<Cloud size={15} />}>Apply SMTP</Button>
              <Link to="/smtp" className="button button-ghost button-md">Manage profiles</Link>
            </div>
          ) : null}
        </Card>

        <Card title="S3-compatible storage" subtitle="Assignment is not application. Apply requires linked PocketBase credentials.">
          <div className="form-grid">
            <Field label="Assigned profile">
              <Select value={storageId} onChange={(event) => setStorageId(event.target.value)}>
                <option value="">No profile</option>
                {storageProfiles.map((profile) => <option value={profile.id} key={profile.id}>{profile.name} · {profile.bucket}</option>)}
              </Select>
            </Field>
            <div className="field">
              <span className="field-label">Pipeline status</span>
              <div className="pipeline">
                <Badge tone={statusTone(storageProfiles.find((p) => p.id === storageId)?.status)}>{storageProfiles.find((p) => p.id === storageId)?.status ?? "profile not selected"}</Badge>
                <span>→</span>
                <Badge tone={statusTone(instance.storageApplyStatus)}>{instance.storageApplyStatus}</Badge>
              </div>
              <div className="field-hint">
                Credentials are validated independently. “Applied” means PocketBase accepted and read back the settings.
              </div>
            </div>
          </div>
          {canWrite ? (
            <div className="button-row">
              <Button variant="secondary" onClick={() => void saveAssignments()} loading={saving} icon={<Save size={15} />}>Save assignments</Button>
              <Button variant="primary" onClick={() => void apply("storage")} disabled={!instance.storageProfileId} icon={<HardDrive size={15} />}>Apply storage</Button>
              <Link to="/storage" className="button button-ghost button-md">Manage profiles</Link>
            </div>
          ) : null}
        </Card>
      </div>
      <aside className="detail-side">
        <Card title="Feature availability">
          <ul className="feature-list">
            <li><CheckCircle2 size={16} /> systemd lifecycle <Badge tone="success">available</Badge></li>
            <li><CheckCircle2 size={16} /> Caddy routing <Badge tone={data.capabilities.caddy ? "success" : "neutral"}>{data.capabilities.caddy ? "available" : "unavailable"}</Badge></li>
            <li><CheckCircle2 size={16} /> DNS automation <Badge tone={data.capabilities.dns ? "success" : "neutral"}>{data.capabilities.dns ? "configured" : "not configured"}</Badge></li>
            <li><CheckCircle2 size={16} /> PocketBase settings API <Badge tone={data.capabilities.pocketbaseAuth ? "success" : "warning"}>{data.capabilities.pocketbaseAuth ? "linked" : "requires superuser"}</Badge></li>
          </ul>
        </Card>
        <Card title="Native backup availability">
          <p className="muted small">Backups use PocketBase's official API when a linked superuser is available. Otherwise the manager uses a consistent SQLite snapshot and archives uploads separately.</p>
          <div className="method-pill">{instance.pbAuthStatus === "linked" ? "PocketBase native + snapshot fallback" : "SQLite snapshot"}</div>
        </Card>
      </aside>
    </div>
  );
}

function BackupsTab({
  backups,
  instance,
  canWrite,
  onRefresh,
  onJob,
  onDownload,
  onVerify,
  onRestore,
  onSchedule,
}: {
  backups: Backup[];
  instance: import("../types").Instance;
  canWrite: boolean;
  onRefresh: () => void;
  onJob: (job: Job) => void;
  onDownload: (backup: Backup) => void;
  onVerify: (backup: Backup) => void;
  onRestore: (backup: Backup) => void;
  onSchedule: () => void;
}) {
  const toast = useToast();
  const [creating, setCreating] = useState(false);

  async function createBackup() {
    setCreating(true);
    try {
      const result = await api.createBackup(instance.id, "Manual backup from instance detail");
      onJob(result.job);
      toast.success("Backup queued");
    } catch (err) {
      toast.error("Could not queue backup", errorMessage(err));
    } finally {
      setCreating(false);
    }
  }

  return (
    <Card
      title="Backup history"
      subtitle={`${instance.backupEnabled ? `Scheduled ${instance.backupCron} UTC · keep ${instance.backupRetention}` : "Scheduled backups disabled"}`}
      actions={
        canWrite ? (
          <div className="button-row">
            <Button size="sm" variant="ghost" onClick={onSchedule}>Schedule</Button>
            <Button size="sm" variant="primary" icon={<Archive size={15} />} onClick={() => void createBackup()} loading={creating}>Back up now</Button>
          </div>
        ) : null
      }
      padded={false}
    >
      {backups.length === 0 ? (
        <EmptyState icon={<Archive size={24} />} title="No backups" description="Create a manual backup or enable a schedule." />
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead><tr><th>Created</th><th>Method</th><th>Status</th><th>Size</th><th>Verified</th><th>Type</th><th /></tr></thead>
            <tbody>
              {backups.map((backup) => (
                <tr key={backup.id}>
                  <td>
                    <strong>{formatDate(backup.createdAt)}</strong>
                    <small className="muted mono">{backup.filename}</small>
                  </td>
                  <td><Badge tone={backup.method === "pocketbase_native" ? "success" : "info"}>{backup.method === "pocketbase_native" ? "PocketBase native" : "SQLite snapshot"}</Badge></td>
                  <td><StatusBadge status={backup.status} />{backup.error ? <small className="text-danger">{backup.error}</small> : null}</td>
                  <td>{formatBytes(backup.size)}</td>
                  <td className="muted">{backup.verifiedAt ? formatRelative(backup.verifiedAt) : "—"}</td>
                  <td className="muted">{backup.automatic ? "scheduled" : "manual"}</td>
                  <td className="table-actions">
                    <Button size="sm" variant="ghost" icon={<Download size={14} />} onClick={() => onDownload(backup)}>Download</Button>
                    {canWrite ? <Button size="sm" variant="ghost" onClick={() => onVerify(backup)}>Verify</Button> : null}
                    {canWrite ? <Button size="sm" variant="ghost" onClick={() => onRestore(backup)} disabled={!["completed", "verified"].includes(backup.status)}>Restore</Button> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function DangerTab({ instance, onDelete }: { instance: import("../types").Instance; onDelete: () => void }) {
  return (
    <Card title="Danger zone" subtitle="Safeguards are intentionally explicit.">
      <div className="danger-row">
        <div>
          <strong>Delete installation from the manager</strong>
          <p>
            Stops and disables the managed service, optionally removes managed Caddy routes and DNS records, and removes
            the inventory record. Application data is never recursively deleted; with purge enabled it is moved to the
            protected purge archive after a final backup.
          </p>
          <div className="inline-badges">
            <Badge tone="neutral">{instance.managed ? "managed unit" : "imported unit preserved"}</Badge>
            <Badge tone={instance.pbAuthStatus === "linked" ? "success" : "warning"}>PB auth {instance.pbAuthStatus}</Badge>
          </div>
        </div>
        <Button variant="danger" icon={<Trash2 size={16} />} onClick={onDelete}>Delete instance…</Button>
      </div>
    </Card>
  );
}

function UpdateModal({
  open,
  instanceId,
  currentVersion,
  onClose,
  onQueued,
}: {
  open: boolean;
  instanceId: string;
  currentVersion: string;
  onClose: () => void;
  onQueued: (job: Job) => void;
}) {
  const [version, setVersion] = useState(currentVersion);
  const [checksum, setChecksum] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setVersion(currentVersion);
      setChecksum("");
      setError(null);
    }
  }, [open, currentVersion]);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const result = await api.updatePocketBase(instanceId, version, checksum);
      onQueued(result.job);
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
      title="Update PocketBase"
      description="A safety backup is created before the binary is replaced."
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" onClick={() => void submit()} loading={busy}>Start update</Button></>}
    >
      <div className="stack">
        {error ? <div className="form-error">{error}</div> : null}
        <Field label="Target version" required hint={`Current version: ${currentVersion || "unknown"}`}>
          <Input value={version} onChange={(event) => setVersion(event.target.value)} placeholder="0.23.6" autoFocus />
        </Field>
        <Field label="Optional SHA-256" hint="Required if trusted release metadata is unavailable.">
          <Input value={checksum} onChange={(event) => setChecksum(event.target.value)} className="mono" />
        </Field>
        <Notice tone="warning">The instance is stopped briefly. The previous binary is retained and automatically restored if the new version fails its health check.</Notice>
      </div>
    </Modal>
  );
}

function CredentialsModal({
  open,
  instanceId,
  email,
  onClose,
  onSaved,
}: {
  open: boolean;
  instanceId: string;
  email: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [form, setForm] = useState({ email, password: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setForm({ email, password: "" });
      setError(null);
    }
  }, [open, email]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.setCredentials(instanceId, form.email.trim(), form.password);
      toast.success("PocketBase credentials verified and stored encrypted");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Link PocketBase superuser" description="Credentials are verified before storage and encrypted at rest." footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" type="submit" form="credentials-form" loading={busy}>Verify & link</Button></>}>
      <form id="credentials-form" className="stack" onSubmit={(event) => void submit(event)}>
        {error ? <div className="form-error">{error}</div> : null}
        <Field label="Superuser email" required>
          <Input type="email" value={form.email} onChange={(event) => setForm({ ...form, email: event.target.value })} autoComplete="username" required />
        </Field>
        <Field label="Superuser password" required hint="Never logged or returned by the API.">
          <Input type="password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} autoComplete="current-password" required />
        </Field>
        <Notice tone="info">Use a dedicated PocketBase superuser for management. The manager only uses documented auth and settings endpoints.</Notice>
      </form>
    </Modal>
  );
}

function ScheduleModal({
  open,
  instance,
  onClose,
  onSaved,
}: {
  open: boolean;
  instance: import("../types").Instance;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [enabled, setEnabled] = useState(instance.backupEnabled);
  const [cron, setCron] = useState(instance.backupCron);
  const [retention, setRetention] = useState(instance.backupRetention);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setEnabled(instance.backupEnabled);
      setCron(instance.backupCron);
      setRetention(instance.backupRetention);
      setError(null);
    }
  }, [open, instance.backupEnabled, instance.backupCron, instance.backupRetention]);

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await api.updateInstanceSettings(instance.id, { backupEnabled: enabled, backupCron: cron, backupRetention: retention });
      toast.success("Backup schedule saved");
      onSaved();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Backup schedule" description="Five-field cron in UTC." footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" onClick={() => void save()} loading={busy}>Save schedule</Button></>}>
      <div className="stack">
        {error ? <div className="form-error">{error}</div> : null}
        <Switch checked={enabled} onChange={setEnabled} label="Scheduled backups" description="A persistent backup job is queued when due." />
        <Field label="Cron expression (UTC)" hint="Example: 0 3 * * *">
          <Input value={cron} onChange={(event) => setCron(event.target.value)} className="mono" />
        </Field>
        <Field label="Retention" hint="Verified backups to keep.">
          <Input type="number" value={retention} onChange={(event) => setRetention(Number(event.target.value))} />
        </Field>
      </div>
    </Modal>
  );
}

function DetachDomainModal({
  domain,
  onClose,
  onQueued,
}: {
  domain: Domain | null;
  onClose: () => void;
  onQueued: (job: Job) => void;
}) {
  const [removeCaddy, setRemoveCaddy] = useState(true);
  const [removeDns, setRemoveDns] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (domain) {
      setRemoveCaddy(domain.caddyManaged);
      setRemoveDns(domain.dnsManaged);
      setConfirm("");
      setError(null);
    }
  }, [domain]);

  async function submit() {
    if (!domain) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.detachDomain(domain.id, { confirmation: confirm, removeCaddy, removeDns });
      onQueued(result.job);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={Boolean(domain)}
      onClose={onClose}
      title="Remove domain"
      size="sm"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="danger" onClick={() => void submit()} loading={busy} disabled={confirm !== domain?.hostname}>Remove domain</Button></>}
    >
      {domain ? (
        <div className="stack">
          {error ? <div className="form-error">{error}</div> : null}
          <p>Remove <strong>{domain.hostname}</strong> from this instance?</p>
          {domain.caddyManaged ? <Switch checked={removeCaddy} onChange={setRemoveCaddy} label="Remove managed Caddy route" /> : null}
          {domain.dnsManaged ? <Switch checked={removeDns} onChange={setRemoveDns} label="Remove manager-owned DNS record" /> : null}
          <Field label={`Type ${domain.hostname} to confirm`}>
            <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} />
          </Field>
        </div>
      ) : null}
    </Modal>
  );
}

function RestoreBackupModal({
  backup,
  instanceName,
  onClose,
  onQueued,
}: {
  backup: Backup | null;
  instanceName: string;
  onClose: () => void;
  onQueued: (job: Job) => void;
}) {
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (backup) {
      setConfirm("");
      setError(null);
    }
  }, [backup]);

  async function submit() {
    if (!backup) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.restoreBackup(backup.id, confirm);
      onQueued(result.job);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={Boolean(backup)}
      onClose={onClose}
      title="Restore backup"
      size="sm"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="danger" onClick={() => void submit()} loading={busy} disabled={confirm !== instanceName}>Restore instance</Button></>}
    >
      {backup ? (
        <div className="stack">
          {error ? <div className="form-error">{error}</div> : null}
          <Notice tone="danger" title="The service will stop">
            The manager verifies the artifact, creates a pre-restore safety backup, stops the service, extracts the
            backup into a new data directory, starts the service, and verifies health. The previous data directory is
            retained.
          </Notice>
          <KeyValue items={[{ label: "Backup", value: backup.filename, mono: true }, { label: "Created", value: formatDate(backup.createdAt) }, { label: "Size", value: formatBytes(backup.size) }]} />
          <Field label={`Type ${instanceName} to confirm`}>
            <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} />
          </Field>
        </div>
      ) : null}
    </Modal>
  );
}

function DeleteInstanceModal({
  open,
  instance,
  domains,
  onClose,
  onQueued,
}: {
  open: boolean;
  instance: import("../types").Instance;
  domains: Domain[];
  onClose: () => void;
  onQueued: (job: Job) => void;
}) {
  const [confirm, setConfirm] = useState("");
  const [purgeData, setPurgeData] = useState(false);
  const [removeCaddy, setRemoveCaddy] = useState(domains.some((domain) => domain.caddyManaged));
  const [removeDns, setRemoveDns] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setConfirm("");
      setPurgeData(false);
      setRemoveCaddy(domains.some((domain) => domain.caddyManaged));
      setRemoveDns(false);
      setError(null);
    }
  }, [open, domains]);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const result = await api.deleteInstance(instance.id, { confirmation: confirm, purgeData, removeCaddy, removeDns });
      onQueued(result.job);
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
      title={`Delete ${instance.name}`}
      size="md"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="danger" onClick={() => void submit()} loading={busy} disabled={confirm !== instance.name}>Queue deletion</Button></>}
    >
      <div className="stack">
        {error ? <div className="form-error">{error}</div> : null}
        <Notice tone="danger" title="Data is never recursively deleted">
          With purge enabled, the manager first creates a final backup, then moves the data directory into the protected
          purge archive. Without purge, application data remains untouched.
        </Notice>
        <Switch checked={removeCaddy} onChange={setRemoveCaddy} label="Remove managed Caddy routes" description="Only pbmanager-owned snippets are removed." />
        <Switch checked={removeDns} onChange={setRemoveDns} label="Remove manager-owned DNS records" description="Requires the Cloudflare token to still be valid." />
        <Switch checked={purgeData} onChange={setPurgeData} label="Archive application data" description="Moves data to backupDir/purged; does not delete it." />
        <Field label={`Type ${instance.name} to confirm`}>
          <Input value={confirm} onChange={(event) => setConfirm(event.target.value)} autoFocus />
        </Field>
      </div>
    </Modal>
  );
}
