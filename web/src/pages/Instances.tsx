import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Boxes, Compass, Download, Plus, RefreshCw, ServerCog } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useDebouncedValue, useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatDate, titleCase } from "../lib/format";
import type { Discovered } from "../types";
import {
  Badge,
  Button,
  Card,
  ErrorBanner,
  Field,
  Input,
  LoadingBlock,
  Modal,
  PageHeader,
  SearchInput,
  Select,
  StatusBadge,
  Textarea,
} from "../components/ui";
import { InstanceTable } from "../components/InstanceTable";

export function InstancesPage() {
  useDocumentTitle("PocketBase Instances");
  const navigate = useNavigate();
  const toast = useToast();
  const [search, setSearch] = useState("");
  const query = useDebouncedValue(search, 250);
  const [status, setStatus] = useState("");
  const { data, error, loading, refresh } = usePolling(
    (signal) => api.instances({ q: query, status, perPage: 200 }),
    10000,
    [query, status],
  );
  const [discoverOpen, setDiscoverOpen] = useState(false);
  const [importCandidate, setImportCandidate] = useState<Discovered | null>(null);
  const [manualImport, setManualImport] = useState(false);

  const instances = data?.items ?? [];

  return (
    <div className="page">
      <PageHeader
        eyebrow="PocketBase"
        title="Instances"
        description="Manage the full lifecycle of PocketBase installations without modifying them unexpectedly."
        actions={
          <>
            <Button
              variant="secondary"
              icon={<Compass size={16} />}
              onClick={() => setDiscoverOpen(true)}
            >
              Discover installations
            </Button>
            <Button
              variant="secondary"
              icon={<Download size={16} />}
              onClick={() => {
                setImportCandidate(null);
                setManualImport(true);
              }}
            >
              Manual import
            </Button>
            <Button variant="primary" icon={<Plus size={16} />} onClick={() => navigate("/instances/new")}>
              New instance
            </Button>
          </>
        }
      />

      <Card padded={false}>
        <div className="toolbar">
          <div className="toolbar-left">
            <SearchInput value={search} onChange={setSearch} placeholder="Search by name, hostname, or notes…" />
            <Select value={status} onChange={(event) => setStatus(event.target.value)} className="toolbar-select">
              <option value="">All statuses</option>
              <option value="running">Running</option>
              <option value="stopped">Stopped</option>
              <option value="failed">Failed</option>
              <option value="provisioning">Provisioning</option>
              <option value="unknown">Unknown</option>
            </Select>
          </div>
          <div className="toolbar-right">
            <span className="muted">{data?.total ?? 0} instances</span>
            <Button size="sm" variant="ghost" icon={<RefreshCw size={15} />} onClick={refresh}>
              Refresh
            </Button>
          </div>
        </div>
        {error ? <div className="card-inline-error"><ErrorBanner message={error} onRetry={refresh} /></div> : null}
        {loading && !data ? (
          <LoadingBlock label="Loading instances…" />
        ) : instances.length === 0 ? (
          <div className="empty-panel">
            <Boxes size={30} />
            <h3>No PocketBase instances match</h3>
            <p>Use discovery to import existing sites, or provision a new installation.</p>
            <div className="button-row">
              <Button onClick={() => setDiscoverOpen(true)} icon={<Compass size={16} />}>
                Discover existing
              </Button>
              <Link to="/instances/new" className="button button-primary button-md">
                <Plus size={16} /> <span>New instance</span>
              </Link>
            </div>
          </div>
        ) : (
          <InstanceTable instances={instances} />
        )}
      </Card>

      <DiscoverModal
        open={discoverOpen}
        onClose={() => setDiscoverOpen(false)}
        onImport={(candidate) => {
          setDiscoverOpen(false);
          setImportCandidate(candidate);
          setManualImport(true);
        }}
      />
      <ImportModal
        open={manualImport}
        candidate={importCandidate}
        onClose={() => setManualImport(false)}
        onImported={(id) => {
          setManualImport(false);
          toast.success("Installation imported", "No files, services, or routes were modified.");
          navigate(`/instances/${id}`);
        }}
      />
    </div>
  );
}

function DiscoverModal({
  open,
  onClose,
  onImport,
}: {
  open: boolean;
  onClose: () => void;
  onImport: (candidate: Discovered) => void;
}) {
  const [items, setItems] = useState<Discovered[] | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run() {
    setLoading(true);
    setError(null);
    try {
      const result = await api.discover();
      setItems(result.items ?? []);
      setWarnings(result.warnings ?? []);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Discover installations"
      description="Read-only inspection of processes, systemd units, data directories, listening ports, and Caddy routes."
      size="xl"
      footer={
        <>
          <Button onClick={onClose}>Close</Button>
          <Button variant="primary" onClick={() => void run()} loading={loading} icon={<Compass size={16} />}>
            {items ? "Scan again" : "Start discovery"}
          </Button>
        </>
      }
    >
      {error ? <ErrorBanner message={error} onRetry={() => void run()} /> : null}
      {warnings.map((warning) => (
        <div className="discovery-warning" key={warning}>
          {warning}
        </div>
      ))}
      {loading ? (
        <LoadingBlock label="Scanning server for PocketBase installations…" />
      ) : !items ? (
        <div className="discovery-intro">
          <Compass size={32} />
          <h3>Inspect this server without changing it</h3>
          <p>
            Discovery only reads existing state. Importing records the installation in the inventory; it does not edit
            the service, data, Caddy routes, or DNS.
          </p>
        </div>
      ) : items.length === 0 ? (
        <div className="empty-panel">
          <ServerCog size={28} />
          <h3>No installations found</h3>
          <p>Try manual import if the installation uses a non-standard path.</p>
        </div>
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Candidate</th>
                <th>Evidence</th>
                <th>Port</th>
                <th>Service</th>
                <th>State</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.key}>
                  <td>
                    <div className="table-primary">
                      <span className="instance-avatar">{item.name.slice(0, 2).toUpperCase()}</span>
                      <span>
                        <strong>{item.name}</strong>
                        <small className="mono">{item.dataDir || "unknown data directory"}</small>
                      </span>
                    </div>
                  </td>
                  <td>
                    <div className="badge-stack">
                      {(item.evidence ?? []).slice(0, 2).map((evidence) => (
                        <Badge tone="neutral" key={evidence}>
                          {evidence}
                        </Badge>
                      ))}
                      {item.domains?.length ? <Badge tone="info">{item.domains.join(", ")}</Badge> : null}
                    </div>
                  </td>
                  <td className="mono">{item.port || "—"}</td>
                  <td>
                    <span className="mono small">{item.serviceName || "—"}</span>
                  </td>
                  <td>
                    {item.tracked ? (
                      <Badge tone="info">Tracked</Badge>
                    ) : item.running ? (
                      <Badge tone="success" dot>Running</Badge>
                    ) : (
                      <Badge tone="neutral">Detected</Badge>
                    )}
                    {item.warnings?.length ? <div className="muted small">{item.warnings.join(" · ")}</div> : null}
                  </td>
                  <td className="table-action">
                    <Button
                      size="sm"
                      variant={item.tracked ? "ghost" : "secondary"}
                      disabled={item.tracked || !item.port}
                      onClick={() => onImport(item)}
                    >
                      {item.tracked ? "Imported" : "Review & import"}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Modal>
  );
}

function ImportModal({
  open,
  candidate,
  onClose,
  onImported,
}: {
  open: boolean;
  candidate: Discovered | null;
  onClose: () => void;
  onImported: (id: string) => void;
}) {
  const [form, setForm] = useState({
    name: "",
    hostname: "",
    baseDir: "",
    binaryPath: "",
    dataDir: "",
    port: 0,
    serviceName: "",
    serviceFile: "",
    runAsUser: "",
    pbVersion: "",
    domains: "",
    notes: "",
  });
  const [initializedFor, setInitializedFor] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();

  const key = candidate?.key ?? "__manual__";

  useEffect(() => {
    if (!open) return;
    setInitializedFor(key);
    setForm({
      name: candidate?.name ?? "",
      hostname: candidate?.hostname ?? candidate?.name ?? "",
      baseDir: candidate?.baseDir ?? "",
      binaryPath: candidate?.binaryPath ?? "",
      dataDir: candidate?.dataDir ?? "",
      port: candidate?.port ?? 0,
      serviceName: candidate?.serviceName ?? "",
      serviceFile: candidate?.serviceFile ?? "",
      runAsUser: candidate?.runAsUser ?? "",
      pbVersion: candidate?.version ?? "",
      domains: candidate?.domains?.join(", ") ?? "",
      notes: "",
    });
    setError(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, key]);

  function update<K extends keyof typeof form>(key: K, value: (typeof form)[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const result = await api.importInstance({
        ...form,
        port: Number(form.port),
        domains: form.domains.split(",").map((domain) => domain.trim()).filter(Boolean),
        source: candidate ? "discovered" : "manual",
      });
      toast.success("Installation imported");
      onImported(result.instance.id);
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
      title={candidate ? "Review discovered installation" : "Register existing installation"}
      description="Importing is an inventory operation only. It never restarts, migrates, or rewrites the installation."
      size="lg"
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="import-form" loading={busy}>
            Import installation
          </Button>
        </>
      }
    >
      <form onSubmit={(event) => void submit(event)} className="form-grid" id="import-form">
        {error ? <div className="form-error form-span">{error}</div> : null}
        <Field label="Instance name" required>
          <Input value={form.name} onChange={(e) => update("name", e.target.value)} required placeholder="clinic-a" />
        </Field>
        <Field label="Hostname identifier" required>
          <Input value={form.hostname} onChange={(e) => update("hostname", e.target.value)} required />
        </Field>
        <Field label="Base directory">
          <Input value={form.baseDir} onChange={(e) => update("baseDir", e.target.value)} className="mono" />
        </Field>
        <Field label="PocketBase binary" required>
          <Input value={form.binaryPath} onChange={(e) => update("binaryPath", e.target.value)} className="mono" required />
        </Field>
        <Field label="Data directory" required>
          <Input value={form.dataDir} onChange={(e) => update("dataDir", e.target.value)} className="mono" required />
        </Field>
        <Field label="Port" required>
          <Input type="number" value={form.port || ""} onChange={(e) => update("port", Number(e.target.value))} required />
        </Field>
        <Field label="Systemd service">
          <Input value={form.serviceName} onChange={(e) => update("serviceName", e.target.value)} />
        </Field>
        <Field label="Service file">
          <Input value={form.serviceFile} onChange={(e) => update("serviceFile", e.target.value)} className="mono" />
        </Field>
        <Field label="Run-as user">
          <Input value={form.runAsUser} onChange={(e) => update("runAsUser", e.target.value)} />
        </Field>
        <Field label="PocketBase version">
          <Input value={form.pbVersion} onChange={(e) => update("pbVersion", e.target.value)} />
        </Field>
        <Field label="Existing domains" hint="Comma-separated. Recorded as unmanaged inventory only.">
          <Input value={form.domains} onChange={(e) => update("domains", e.target.value)} />
        </Field>
        <Field label="Notes">
          <Input value={form.notes} onChange={(e) => update("notes", e.target.value)} />
        </Field>
        {candidate?.evidence?.length ? (
          <div className="form-span discovery-evidence">
            <strong>Discovery evidence:</strong>
            {candidate.evidence.map((item) => (
              <Badge tone="neutral" key={item}>{item}</Badge>
            ))}
            {candidate.warnings?.length ? <div className="muted">Warnings: {candidate.warnings.join("; ")}</div> : null}
          </div>
        ) : null}
        <div className="form-span">
          <Textarea
            placeholder="Optional discovery JSON is stored for auditability."
            value={candidate ? JSON.stringify(candidate, null, 2) : ""}
            readOnly
            hidden={!candidate}
            aria-label="Discovery metadata"
          />
        </div>
      </form>
    </Modal>
  );
}
