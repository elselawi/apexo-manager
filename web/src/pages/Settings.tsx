import { useEffect, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Compass,
  Database,
  FolderCog,
  Globe2,
  Network,
  RefreshCw,
  Save,
  Settings2,
  ShieldCheck,
  Timer,
} from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import type { ServerConfig } from "../types";
import {
  Badge,
  Button,
  Card,
  CopyField,
  ErrorBanner,
  Field,
  Input,
  LoadingBlock,
  Notice,
  PageHeader,
  Switch,
} from "../components/ui";

export function SettingsPage() {
  useDocumentTitle("Server Configuration");
  const { admin } = useAuth();
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.settings(), 30000);
  const [form, setForm] = useState<ServerConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  useEffect(() => {
    if (data && !form) setForm(data.config);
  }, [data, form]);

  if (admin?.role !== "owner") {
    return (
      <div className="page">
        <PageHeader eyebrow="System" title="Server configuration" description="Only the owner role can view or modify server-wide settings." />
        <Notice tone="warning" title="Owner permission required">
          Ask the owner administrator to change server paths. Root CLI recovery remains available.
        </Notice>
      </div>
    );
  }
  if (loading && !data) return <LoadingBlock label="Loading server configuration…" />;
  if (!data || !form) return <ErrorBanner message={error ?? "Could not load settings"} onRetry={refresh} />;

  function update<K extends keyof ServerConfig>(key: K, value: ServerConfig[K]) {
    setForm((current) => (current ? { ...current, [key]: value } : current));
  }

  async function save() {
    if (!form) return;
    setBusy(true);
    setFormError(null);
    try {
      const result = await api.updateSettings(form);
      setForm(result.settings.config);
      toast.success("Server configuration saved", result.changed.length ? `Changed: ${result.changed.join(", ")}` : "No defaults changed. Existing installations were not moved.");
      refresh();
    } catch (err) {
      setFormError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function detectIP() {
    setDetecting(true);
    try {
      const result = await api.detectIP();
      update("publicIpv4", result.publicIpv4);
      toast.success("Detected public IPv4", result.publicIpv4);
    } catch (err) {
      toast.error("Could not detect public IPv4", errorMessage(err));
    } finally {
      setDetecting(false);
    }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="System"
        title="Server configuration"
        description="Server-wide defaults apply only to future operations. Existing installations are never relocated implicitly."
        actions={
          <>
            <Button variant="secondary" icon={<RefreshCw size={15} />} onClick={() => { setForm(data.config); setFormError(null); }}>Discard changes</Button>
            <Button variant="primary" icon={<Save size={16} />} onClick={() => void save()} loading={busy}>Save configuration</Button>
          </>
        }
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}
      {formError ? <ErrorBanner message={formError} /> : null}
      {data.warnings?.length ? <Notice tone="warning" title="Operational warnings">{data.warnings.map((warning) => <div key={warning}>{warning}</div>)}</Notice> : null}
      <Notice tone="info" title="Changing a path does not move data">
        These are default templates and server paths. Per-instance paths stay untouched. Explicit migration is required
        before relocating anything.
      </Notice>

      <div className="settings-layout">
        <div className="settings-main">
          <Card title={<span className="settings-card-title"><FolderCog size={18} /> Installation defaults</span>} subtitle="Used only when provisioning a new instance.">
            <div className="form-grid">
              <Field label="Default installation root" hint="Instance defaults use <root>/<name>.">
                <Input value={form.installRoot} onChange={(e) => update("installRoot", e.target.value)} className="mono" />
              </Field>
              <Field label="PocketBase data directory convention" hint="Must contain {base_dir}.">
                <Input value={form.pbDataDirTemplate} onChange={(e) => update("pbDataDirTemplate", e.target.value)} className="mono" />
              </Field>
              <Field label="Archive filename template">
                <Input value={form.archiveTemplate} onChange={(e) => update("archiveTemplate", e.target.value)} className="mono" />
              </Field>
              <Field label="Default PocketBase version">
                <Input value={form.defaultPbVersion} onChange={(e) => update("defaultPbVersion", e.target.value)} />
              </Field>
              <Field label="Download source template" hint="Requires {version} and {arch}. HTTPS is enforced except for loopback test sources.">
                <Input value={form.downloadUrlTemplate} onChange={(e) => update("downloadUrlTemplate", e.target.value)} className="mono" />
              </Field>
              <Field label="Checksum source template" hint="Release checksums.txt. GitHub asset digests are used as a fallback.">
                <Input value={form.checksumUrlTemplate} onChange={(e) => update("checksumUrlTemplate", e.target.value)} className="mono" />
              </Field>
              <Field label="systemd unit directory" hint="New units prefer /etc/systemd/system.">
                <Input value={form.serviceDir} onChange={(e) => update("serviceDir", e.target.value)} className="mono" />
              </Field>
              <Field label="Service user prefix">
                <Input value={form.serviceUserPrefix} onChange={(e) => update("serviceUserPrefix", e.target.value)} />
              </Field>
            </div>
            <Switch checked={form.requireChecksums} onChange={(value) => update("requireChecksums", value)} label="Require trusted release checksums" description="Refuse provisioning when no trusted digest can be obtained." />
          </Card>

          <Card title={<span className="settings-card-title"><Network size={18} /> Caddy</span>} subtitle="Managed snippets preserve unrelated configuration.">
            <div className="form-grid">
              <Field label="Caddyfile">
                <Input value={form.caddyfilePath} onChange={(e) => update("caddyfilePath", e.target.value)} className="mono" />
              </Field>
              <Field label="Managed snippet directory">
                <Input value={form.caddySnippetDir} onChange={(e) => update("caddySnippetDir", e.target.value)} className="mono" />
              </Field>
              <Field label="Caddy service name">
                <Input value={form.caddyService} onChange={(e) => update("caddyService", e.target.value)} />
              </Field>
              <Field label="Caddy binary">
                <Input value={form.caddyBinary} onChange={(e) => update("caddyBinary", e.target.value)} className="mono" />
              </Field>
            </div>
            <div className="button-row">
              <Button size="sm" variant="ghost" onClick={() => update("defaultCaddyEnabled", !form.defaultCaddyEnabled)}>
                Default route management: {form.defaultCaddyEnabled ? "enabled" : "disabled"}
              </Button>
              <Badge tone="info">{`import "${form.caddySnippetDir}/*.caddy"`}</Badge>
            </div>
          </Card>

          <Card title={<span className="settings-card-title"><Database size={18} /> Paths & binaries</span>} subtitle="Validated before saving. The application data directory is bootstrap-owned.">
            <div className="form-grid">
              <Field label="Backup directory">
                <Input value={form.backupDir} onChange={(e) => update("backupDir", e.target.value)} className="mono" />
              </Field>
              <Field label="Public IPv4" hint="Used for managed Cloudflare A records.">
                <div className="input-with-button">
                  <Input value={form.publicIpv4} onChange={(e) => update("publicIpv4", e.target.value)} className="mono" placeholder="203.0.113.10" />
                  <Button size="sm" variant="secondary" icon={<Compass size={14} />} onClick={() => void detectIP()} loading={detecting}>Detect</Button>
                </div>
              </Field>
              <Field label="systemctl binary">
                <Input value={form.systemctlBinary} onChange={(e) => update("systemctlBinary", e.target.value)} className="mono" />
              </Field>
              <Field label="journalctl binary">
                <Input value={form.journalctlBinary} onChange={(e) => update("journalctlBinary", e.target.value)} className="mono" />
              </Field>
              <Field label="Discovery roots" hint="Comma-separated absolute paths scanned read-only.">
                <Input value={form.discoveryRoots.join(", ")} onChange={(e) => update("discoveryRoots", e.target.value.split(",").map((v) => v.trim()).filter(Boolean))} className="mono" />
              </Field>
            </div>
            <div className="readonly-bootstrap">
              <div><span>Application data directory</span><CopyField value={data.bootstrap.dataDir} /></div>
              <p>Bootstrap values are root-owned and cannot be changed from the web UI. Use the CLI config workflow and restart the service; data is never moved silently.</p>
            </div>
          </Card>

          <Card title={<span className="settings-card-title"><Timer size={18} /> Ports, retention & monitoring</span>}>
            <div className="form-grid">
              <Field label="Port range start">
                <Input type="number" value={form.portStart} onChange={(e) => update("portStart", Number(e.target.value))} />
              </Field>
              <Field label="Port range end">
                <Input type="number" value={form.portEnd} onChange={(e) => update("portEnd", Number(e.target.value))} />
              </Field>
              <Field label="Monitoring interval (seconds)" hint="Minimum 15 seconds; default 60.">
                <Input type="number" value={form.monitorIntervalSeconds} onChange={(e) => update("monitorIntervalSeconds", Number(e.target.value))} />
              </Field>
              <Field label="Default backup retention">
                <Input type="number" value={form.backupRetentionDefault} onChange={(e) => update("backupRetentionDefault", Number(e.target.value))} />
              </Field>
              <Field label="Job history retention (days)">
                <Input type="number" value={form.jobRetentionDays} onChange={(e) => update("jobRetentionDays", Number(e.target.value))} />
              </Field>
              <Field label="Audit retention (days)">
                <Input type="number" value={form.auditRetentionDays} onChange={(e) => update("auditRetentionDays", Number(e.target.value))} />
              </Field>
            </div>
            <div className="switch-stack">
              <Switch checked={form.verifyHttps} onChange={(value) => update("verifyHttps", value)} label="Verify public HTTPS after provisioning" description="Uses certificate and health checks before reporting success." />
              <Switch checked={form.defaultDnsEnabled} onChange={(value) => update("defaultDnsEnabled", value)} label="Default DNS management for new instances" description="Still requires a selected Cloudflare account and an explicit domain." />
            </div>
          </Card>
        </div>

        <aside className="settings-side">
          <Card title={<span className="settings-card-title"><Settings2 size={18} /> Bootstrap</span>} subtitle="Read-only from the dashboard">
            <div className="stack">
              <div><span className="field-label">Config file</span><CopyField value={data.bootstrap.configPath} /></div>
              <div><span className="field-label">Listen address</span><CopyField value={data.bootstrap.listenAddr} /></div>
              <div><span className="field-label">Public URL</span><CopyField value={data.bootstrap.publicUrl} /></div>
              <div><span className="field-label">Database</span><CopyField value={data.bootstrap.dbPath} /></div>
              <p className="muted small">{data.bootstrap.note}</p>
            </div>
          </Card>
          <Card title={<span className="settings-card-title"><ShieldCheck size={18} /> Validation</span>}>
            <ul className="feature-list">
              <li><CheckCircle2 size={16} /> Absolute, normalized paths</li>
              <li><CheckCircle2 size={16} /> No overwriting existing instances</li>
              <li><CheckCircle2 size={16} /> systemd directories must exist</li>
              <li><CheckCircle2 size={16} /> Caddyfile existence checked</li>
              <li><CheckCircle2 size={16} /> Backup paths protected from traversal</li>
              <li><CheckCircle2 size={16} /> Download URLs restricted to HTTPS</li>
            </ul>
          </Card>
          <Card title={<span className="settings-card-title"><Globe2 size={18} /> Public address</span>}>
            <p className="muted small">DNS records use this IPv4. Detect it from an external echo service with a local-route fallback, or set it manually.</p>
            {form.publicIpv4 ? <Badge tone="success">{form.publicIpv4}</Badge> : <Badge tone="warning">not configured</Badge>}
          </Card>
          <Notice tone="warning" title="Changing server paths">
            Save updates defaults only. Re-run discovery and review imported paths after any infrastructure move.
          </Notice>
        </aside>
      </div>
    </div>
  );
}
