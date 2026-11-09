import { useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  CheckCircle2,
  FileCode2,
  Globe2,
  RefreshCw,
  ScrollText,
  ShieldCheck,
  Terminal,
  Trash2,
  Wrench,
} from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatRelative } from "../lib/format";
import type { Job } from "../types";
import {
  Badge,
  Button,
  Card,
  CodeBlock,
  ConfirmDialog,
  CopyField,
  EmptyState,
  ErrorBanner,
  LoadingBlock,
  Notice,
  PageHeader,
  Select,
  StatusBadge,
} from "../components/ui";
import { JobProgress } from "../components/JobProgress";

export function CaddyPage() {
  useDocumentTitle("Caddy");
  const { admin } = useAuth();
  const canWrite = admin?.role !== "viewer";
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.caddy(), 15000);
  const [jobId, setJobId] = useState<string | null>(null);
  const [validating, setValidating] = useState(false);
  const [logs, setLogs] = useState<string | null>(null);
  const [logLines, setLogLines] = useState(200);
  const [logsLoading, setLogsLoading] = useState(false);
  const [removeDomain, setRemoveDomain] = useState<string | null>(null);
  const [removing, setRemoving] = useState(false);

  if (loading && !data) return <LoadingBlock label="Inspecting Caddy…" />;
  if (!data) return <ErrorBanner message={error ?? "Could not inspect Caddy"} onRetry={refresh} />;

  const status = data.status;
  const sites = data.sites ?? [];
  const allSnippets = data.snippets ?? [];
  const managedSnippets = allSnippets.filter((snippet) => snippet.managed);
  const unmanagedSnippets = allSnippets.filter((snippet) => !snippet.managed);

  async function ensureImport() {
    try {
      const result = await api.caddyEnsureImport();
      setJobId(result.job.id);
      toast.success("Caddy import job queued");
    } catch (err) {
      toast.error("Could not queue import", errorMessage(err));
    }
  }

  async function validate() {
    setValidating(true);
    try {
      const result = await api.caddyValidate();
      if (result.valid) toast.success("Caddyfile is valid");
      else toast.error("Caddyfile validation failed", result.error);
      refresh();
    } catch (err) {
      toast.error("Validation failed", errorMessage(err));
    } finally {
      setValidating(false);
    }
  }

  async function removeRoute() {
    if (!removeDomain) return;
    setRemoving(true);
    try {
      await api.caddyRemoveRoute(removeDomain);
      toast.success("Managed Caddy route removed", removeDomain);
      setRemoveDomain(null);
      refresh();
    } catch (err) {
      toast.error("Could not remove route", errorMessage(err));
    } finally {
      setRemoving(false);
    }
  }

  async function loadLogs() {    setLogsLoading(true);
    try {
      const result = await api.caddyLogs(logLines);
      setLogs(result.logs || "No Caddy journal entries returned.");
    } catch (err) {
      setLogs(`Could not load Caddy logs:\n${errorMessage(err)}`);
    } finally {
      setLogsLoading(false);
    }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Reverse proxy"
        title="Caddy"
        description="Inspect existing sites, manage dedicated snippets, and validate every change before reload."
        actions={
          canWrite ? (
            <>
              <Button variant="secondary" icon={<CheckCircle2 size={15} />} onClick={() => void validate()} loading={validating}>Validate Caddyfile</Button>
              <Button variant="secondary" icon={<Wrench size={15} />} onClick={() => void ensureImport()} disabled={status.importInstalled}>Install managed import</Button>
            </>
          ) : null
        }
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}
      {jobId ? <JobProgress jobId={jobId} dismissible onDismiss={() => setJobId(null)} onFinished={() => refresh()} /> : null}

      <div className="status-banner">
        <div className={`status-banner-icon ${status.active ? "ok" : "bad"}`}>{status.active ? <CheckCircle2 size={22} /> : <AlertTriangle size={22} />}</div>
        <div>
          <strong>{status.active ? "Caddy service is active" : "Caddy service is not active"}</strong>
          <span>{status.version ?? "Version unavailable"} · {status.configValid ? "configuration valid" : "configuration not validated"}</span>
          {status.error ? <span className="text-danger">{status.error}</span> : null}
        </div>
        <div className="button-row">
          <StatusBadge status={status.configValid ? "valid" : "failed"} label={status.configValid ? "Config valid" : "Config invalid"} />
          <StatusBadge status={status.importInstalled ? "ok" : "missing"} label={status.importInstalled ? "Import installed" : "Import missing"} />
          <Button size="sm" variant="ghost" icon={<RefreshCw size={14} />} onClick={refresh}>Refresh</Button>
        </div>
      </div>

      <div className="two-column-grid">
        <Card title="Configuration files" subtitle="The manager never regenerates the whole Caddyfile.">
          <div className="stack">
            <div>
              <span className="field-label">Caddyfile</span>
              <CopyField value={status.caddyfile} />
            </div>
            <div>
              <span className="field-label">Managed snippet directory</span>
              <CopyField value={status.snippetDir} />
            </div>
            <div>
              <span className="field-label">Import directive</span>
              <div className="code-inline">{`import "${status.snippetDir}/*.caddy"`}</div>
            </div>
            {!status.importInstalled ? (
              <Notice tone="warning" title="Managed snippets are not imported">
                Install the import directive to enable managed routes. The manager backs up the Caddyfile, validates the
                candidate configuration, reloads, and restores on failure.
              </Notice>
            ) : (
              <Notice tone="success" title="Managed snippets are active">
                Existing site blocks and custom directives are left untouched.
              </Notice>
            )}
          </div>
        </Card>

        <Card title="Safety model" subtitle="Every managed change follows the same path.">
          <ol className="process-list">
            <li>Write only a dedicated snippet file, never the entire Caddyfile.</li>
            <li>Stage the full candidate configuration in a temporary snippet directory.</li>
            <li>Run <code>caddy validate</code> against the staged configuration.</li>
            <li>Back up the Caddyfile and every snippet.</li>
            <li>Apply atomically and gracefully reload Caddy.</li>
            <li>If reload fails, restore the previous configuration and reload again.</li>
          </ol>
          <div className="button-row">
            <Link to="/domains" className="button button-secondary button-md"><Globe2 size={15} /> Manage domains</Link>
          </div>
        </Card>
      </div>

      <Card title="Site blocks" subtitle={`${sites.length} top-level sites parsed from the Caddyfile`} padded={false}>
        {sites.length === 0 ? (
          <EmptyState icon={<FileCode2 size={24} />} title="No top-level sites parsed" description="The Caddyfile may use a structure the read-only parser does not recognize, or it may be managed entirely by snippets." />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead><tr><th>Addresses</th><th>Upstreams</th><th>Ownership</th><th>Line</th></tr></thead>
              <tbody>
                {sites.map((site, index) => (
                  <tr key={`${site.addresses.join(",")}-${index}`}>
                    <td><strong>{site.addresses.join(", ") || "—"}</strong></td>
                    <td className="mono small">{site.upstreams.join(", ") || "—"}</td>
                    <td>{site.managed ? <Badge tone="success">managed</Badge> : <Badge tone="neutral">existing</Badge>}</td>
                    <td className="muted">{site.line}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card title="Managed snippets" subtitle={`${managedSnippets.length} pbmanager-owned files`} padded={false}>
        {managedSnippets.length === 0 ? (
          <EmptyState icon={<ShieldCheck size={24} />} title="No managed snippets" description="Attach a domain with Caddy management enabled to create one." />
        ) : (
          <div className="snippet-grid">
            {managedSnippets.map((snippet) => (
              <div className="snippet-card" key={snippet.path}>
                <div className="snippet-head"><FileCode2 size={18} /><strong>{snippet.name}</strong><Badge tone="success">managed</Badge></div>
                <div className="snippet-domains">{snippet.domains.join(", ") || "—"}</div>
                <div className="muted small">→ {snippet.upstreams.join(", ") || "no upstream parsed"}</div>
                <div className="snippet-footer">
                  <span className="muted small">Modified {formatRelative(snippet.modifiedAt)}</span>
                  {canWrite && snippet.domains[0] ? (
                    <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={() => setRemoveDomain(snippet.domains[0])}>Remove</Button>
                  ) : null}
                </div>
              </div>
            ))}
          </div>
        )}
        {unmanagedSnippets.length > 0 ? (
          <div className="unmanaged-note">
            <AlertTriangle size={15} /> {unmanagedSnippets.length} unmanaged snippet files exist in this directory and are preserved.
          </div>
        ) : null}
      </Card>

      <Card
        title="Caddy logs"
        subtitle="Recent journal output"
        actions={
          <div className="button-row">
            <Select value={logLines} onChange={(event) => setLogLines(Number(event.target.value))} className="compact-select">
              <option value={100}>100 lines</option><option value={200}>200 lines</option><option value={500}>500 lines</option>
            </Select>
            <Button size="sm" variant="secondary" icon={<ScrollText size={14} />} onClick={() => void loadLogs()} loading={logsLoading}>Load logs</Button>
          </div>
        }
      >
        {logs === null ? (
          <EmptyState icon={<Terminal size={24} />} title="Logs not loaded" description="Read the Caddy journal on demand." compact />
        ) : (
          <CodeBlock maxHeight={460}>{logs}</CodeBlock>
        )}
      </Card>

      <ConfirmDialog
        open={removeDomain !== null}
        onClose={() => setRemoveDomain(null)}
        onConfirm={() => void removeRoute()}
        title="Remove managed Caddy route"
        message={<p>This removes only the pbmanager-owned snippet for <code>{removeDomain}</code>. The Caddyfile, other sites, DNS records, and PocketBase data are untouched.</p>}
        confirmLabel="Remove route"
        requireText={removeDomain ?? ""}
        danger
        busy={removing}
      />
    </div>
  );
}
