import { useState } from "react";
import { ScrollText, RefreshCw } from "lucide-react";
import { api } from "../lib/api";
import { useDocumentTitle, usePolling, useDebouncedValue } from "../lib/hooks";
import { formatDate, titleCase } from "../lib/format";
import {
  Badge,
  Button,
  Card,
  CodeBlock,
  EmptyState,
  ErrorBanner,
  LoadingBlock,
  Modal,
  PageHeader,
  Pagination,
  SearchInput,
  Select,
} from "../components/ui";

export function AuditPage() {
  useDocumentTitle("Activity & Audit");
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<import("../types").AuditEntry | null>(null);
  const debounced = useDebouncedValue(search, 250);
  const { data, error, loading, refresh } = usePolling(
    (signal) => api.audit({ action, actor, q: debounced, page, perPage: 50 }),
    15000,
    [action, actor, debounced, page],
  );

  const entries = data?.items ?? [];

  return (
    <div className="page">
      <PageHeader
        eyebrow="Security"
        title="Activity & audit log"
        description="Append-only records of administrative actions, integrations, backups, and recovery events."
        actions={<Button variant="secondary" icon={<RefreshCw size={15} />} onClick={refresh}>Refresh</Button>}
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      <Card padded={false}>
        <div className="toolbar">
          <div className="toolbar-left">
            <SearchInput value={search} onChange={(value) => { setSearch(value); setPage(1); }} placeholder="Search action, target, or detail…" />
            <Select value={action} onChange={(event) => { setAction(event.target.value); setPage(1); }}>
              <option value="">All actions</option>
              <option value="auth.">Authentication</option>
              <option value="instance.">Instances</option>
              <option value="domain.">Domains</option>
              <option value="backup.">Backups</option>
              <option value="caddy.">Caddy</option>
              <option value="smtp.">SMTP</option>
              <option value="storage.">Storage</option>
              <option value="settings.">Settings</option>
              <option value="admin.">Administrators</option>
              <option value="cloudflare.">Cloudflare</option>
              <option value="job.">Jobs</option>
            </Select>
            <input className="input compact" value={actor} onChange={(event) => { setActor(event.target.value); setPage(1); }} placeholder="Actor username" aria-label="Actor filter" />
          </div>
          <span className="muted">{data?.total ?? 0} entries</span>
        </div>
        {loading && !data ? <LoadingBlock /> : entries.length === 0 ? (
          <EmptyState icon={<ScrollText size={26} />} title="No audit entries" description="Administrative changes and system operations will appear here." />
        ) : (
          <div className="table-wrap">
            <table className="data-table clickable">
              <thead><tr><th>Time</th><th>Action</th><th>Actor</th><th>Target</th><th>Source</th><th /></tr></thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.id} onClick={() => setSelected(entry)} tabIndex={0} onKeyDown={(event) => event.key === "Enter" && setSelected(entry)}>
                    <td className="muted">{formatDate(entry.createdAt)}</td>
                    <td><strong>{titleCase(entry.action.replaceAll(".", " "))}</strong><small className="mono muted">{entry.action}</small></td>
                    <td><Badge tone={entry.actorType === "system" ? "neutral" : "info"}>{entry.actorName || entry.actorType}</Badge></td>
                    <td><span className="muted small">{entry.targetType}</span>{entry.targetId ? <small className="mono muted">{entry.targetId.slice(0, 16)}</small> : null}</td>
                    <td className="muted small">{entry.ip || "—"}</td>
                    <td className="table-action"><Button size="sm" variant="ghost">Details</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="pagination-wrap">
          <span className="muted small">Newest first</span>
          <Pagination page={page} perPage={50} total={data?.total ?? 0} onPage={setPage} />
        </div>
      </Card>

      <Modal open={Boolean(selected)} onClose={() => setSelected(null)} title="Audit entry" size="md" footer={<Button onClick={() => setSelected(null)}>Close</Button>}>
        {selected ? (
          <div className="audit-detail">
            <dl>
              <div><dt>Time</dt><dd>{formatDate(selected.createdAt)}</dd></div>
              <div><dt>Action</dt><dd className="mono">{selected.action}</dd></div>
              <div><dt>Actor</dt><dd>{selected.actorName} ({selected.actorType})</dd></div>
              <div><dt>Target</dt><dd>{selected.targetType} {selected.targetId}</dd></div>
              <div><dt>IP</dt><dd>{selected.ip || "—"}</dd></div>
              <div><dt>Request ID</dt><dd className="mono">{selected.requestId || "—"}</dd></div>
            </dl>
            <div>
              <span className="field-label">Detail</span>
              <CodeBlock maxHeight={360}>{pretty(selected.detail ?? "{}")}</CodeBlock>
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  );
}

function pretty(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
