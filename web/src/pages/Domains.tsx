import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import {
  Cloud,
  ExternalLink,
  Globe2,
  KeyRound,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatDate, formatRelative } from "../lib/format";
import type { CloudflareAccount, DNSRecord, Domain, Instance, Zone } from "../types";
import {
  Badge,
  Button,
  Card,
  CodeBlock,
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
  Switch,
} from "../components/ui";
import { DomainAttachModal } from "../components/DomainAttachModal";

export function DomainsPage() {
  useDocumentTitle("Domains & DNS");
  const { admin } = useAuth();
  const canWrite = admin?.role !== "viewer";
  const toast = useToast();
  const { data, error, loading, refresh } = usePolling((signal) => api.domains(), 15000);
  const { data: instancesData } = usePolling((signal) => api.instances({ perPage: 200 }), 30000);
  const { data: accountsData, refresh: refreshAccounts } = usePolling((signal) => api.cloudflareAccounts(), 30000);
  const [accountOpen, setAccountOpen] = useState(false);
  const [editAccount, setEditAccount] = useState<CloudflareAccount | null>(null);
  const [deleteAccount, setDeleteAccount] = useState<CloudflareAccount | null>(null);
  const [zonesAccount, setZonesAccount] = useState<CloudflareAccount | null>(null);
  const [attachTo, setAttachTo] = useState<Instance | null>(null);
  const [attachOpen, setAttachOpen] = useState(false);
  const [detachDomain, setDetachDomain] = useState<Domain | null>(null);
  const [search, setSearch] = useState("");
  const [checking, setChecking] = useState<string | null>(null);

  const domains = (data?.items ?? []).filter((domain) => {
    const term = search.toLowerCase();
    return !term || domain.hostname.toLowerCase().includes(term) || (domain.instanceName ?? "").toLowerCase().includes(term);
  });
  const accounts = accountsData?.items ?? [];
  const instances = instancesData?.items ?? [];

  async function check(domain: Domain) {
    setChecking(domain.id);
    try {
      const result = await api.checkDomain(domain.id);
      toast.success("Domain checked", `${String(result.dnsStatus)} DNS · ${String(result.tlsStatus)} TLS`);
      refresh();
    } catch (err) {
      toast.error("Check failed", errorMessage(err));
    } finally {
      setChecking(null);
    }
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="Routing"
        title="Domains & DNS"
        description="Map hostnames to instances, manage least-privilege Cloudflare accounts, and verify DNS and TLS."
        actions={
          canWrite ? (
            <Button
              variant="primary"
              icon={<Plus size={16} />}
              onClick={() => document.getElementById("attach-domain-section")?.scrollIntoView({ behavior: "smooth", block: "center" })}
              disabled={instances.length === 0}
            >
              Attach domain
            </Button>
          ) : null
        }
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      <div className="two-column-grid">
        <Card
          title="Domain mappings"
          subtitle={`${domains.length} hostnames`}
          actions={<Button size="sm" variant="ghost" icon={<RefreshCw size={14} />} onClick={refresh}>Refresh</Button>}
          padded={false}
        >
          <div className="toolbar compact">
            <SearchInput value={search} onChange={setSearch} placeholder="Filter domains…" />
          </div>
          {loading && !data ? <LoadingBlock /> : domains.length === 0 ? (
            <EmptyState icon={<Globe2 size={26} />} title="No domains mapped" description="Attach a domain to an instance to manage Caddy and optionally Cloudflare DNS." />
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead><tr><th>Hostname</th><th>Instance</th><th>DNS</th><th>Caddy</th><th>TLS</th><th>Checked</th><th /></tr></thead>
                <tbody>
                  {domains.map((domain) => (
                    <tr key={domain.id}>
                      <td>
                        <strong>{domain.hostname}</strong>
                        <div className="inline-badges">
                          {domain.proxied ? <Badge tone="warning">Cloudflare proxy</Badge> : null}
                          {domain.zoneName ? <span className="muted small">{domain.zoneName}</span> : null}
                        </div>
                      </td>
                      <td><Link to={`/instances/${domain.instanceId}`} className="text-link">{domain.instanceName ?? domain.instanceId}</Link></td>
                      <td><StatusBadge status={domain.dnsStatus} /></td>
                      <td><StatusBadge status={domain.caddyStatus} /></td>
                      <td><StatusBadge status={domain.tlsStatus} /></td>
                      <td className="muted small">{formatRelative(domain.lastCheckedAt)}</td>
                      <td className="table-actions">
                        <Button size="sm" variant="ghost" onClick={() => void check(domain)} loading={checking === domain.id}>Check</Button>
                        {canWrite ? <IconButton label="Detach domain" onClick={() => setDetachDomain(domain)}><Trash2 size={15} /></IconButton> : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>

        <Card
          title="Cloudflare accounts"
          subtitle="Tokens are encrypted at rest and never returned by the API."
          actions={canWrite ? <Button size="sm" variant="secondary" icon={<Plus size={14} />} onClick={() => { setEditAccount(null); setAccountOpen(true); }}>Add account</Button> : null}
        >
          {accounts.length === 0 ? (
            <EmptyState icon={<Cloud size={24} />} title="No Cloudflare account" description="Add a least-privilege API token with Zone:Read and DNS:Edit." compact />
          ) : (
            <div className="account-list">
              {accounts.map((account) => (
                <div key={account.id} className="cloudflare-account">
                  <div className="cloudflare-account-head">
                    <div className="account-icon"><KeyRound size={17} /></div>
                    <div>
                      <strong>{account.name}</strong>
                      <span className="muted small">{account.accountId || "No account ID"}</span>
                    </div>
                    <StatusBadge status={account.status} />
                  </div>
                  <div className="cloudflare-account-meta">
                    <span>Validated {formatRelative(account.lastValidatedAt)}</span>
                    {account.lastError ? <span className="text-danger">{account.lastError}</span> : null}
                  </div>
                  <div className="button-row">
                    <Button size="sm" variant="ghost" onClick={() => setZonesAccount(account)}>Browse zones</Button>
                    {canWrite ? <Button size="sm" variant="ghost" onClick={() => { setEditAccount(account); setAccountOpen(true); }}>Edit</Button> : null}
                    {canWrite ? <Button size="sm" variant="ghost" onClick={() => void api.validateCloudflareAccount(account.id).then((result) => { result.ok ? toast.success("Token valid") : toast.error("Token invalid", result.error); refreshAccounts(); }).catch((err) => toast.error("Validation failed", errorMessage(err)))}>Validate</Button> : null}
                    {canWrite ? <IconButton label="Delete account" onClick={() => setDeleteAccount(account)}><Trash2 size={15} /></IconButton> : null}
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>

      <Card id="attach-domain-section" title="Attach a domain to an instance" subtitle="Choose the target instance before opening the mapping dialog.">
        {instances.length === 0 ? (
          <EmptyState icon={<Globe2 size={24} />} title="No instances" description="Provision or import an instance first." action={<Link to="/instances" className="button button-primary button-md">Open instances</Link>} compact />
        ) : (
          <div className="attach-domain-row">
            <Select
              value={attachTo?.id ?? ""}
              onChange={(event) => setAttachTo(instances.find((instance) => instance.id === event.target.value) ?? null)}
            >
              <option value="">Select an instance</option>
              {instances.map((instance) => <option value={instance.id} key={instance.id}>{instance.name} · port {instance.port}</option>)}
            </Select>
            <Button variant="primary" disabled={!attachTo || !canWrite} onClick={() => setAttachOpen(true)}>
              Open mapping dialog
            </Button>
          </div>
        )}
      </Card>

      <AccountModal
        open={accountOpen}
        account={editAccount}
        onClose={() => setAccountOpen(false)}
        onSaved={() => { refreshAccounts(); toast.success("Cloudflare account saved"); }}
      />
      <ConfirmDialog
        open={Boolean(deleteAccount)}
        onClose={() => setDeleteAccount(null)}
        onConfirm={() => {
          if (!deleteAccount) return;
          void api
            .deleteCloudflareAccount(deleteAccount.id, deleteAccount.name)
            .then(() => { toast.success("Cloudflare account deleted"); setDeleteAccount(null); refreshAccounts(); })
            .catch((err) => toast.error("Could not delete account", errorMessage(err)));
        }}
        title="Delete Cloudflare account"
        message={<>Delete <strong>{deleteAccount?.name}</strong>? This does not delete any DNS records, and is refused if domains still use the account.</>}
        confirmLabel="Delete account"
        danger
        requireText={deleteAccount?.name}
      />
      <DetachDomainDialog domain={detachDomain} onClose={() => setDetachDomain(null)} onDone={() => { setDetachDomain(null); refresh(); }} />
      <ZonesModal account={zonesAccount} onClose={() => setZonesAccount(null)} />
      <DomainAttachModal
        open={attachOpen}
        instanceId={attachTo?.id ?? ""}
        instanceName={attachTo?.name ?? ""}
        onClose={() => { setAttachOpen(false); setAttachTo(null); }}
        onCreated={() => { setAttachOpen(false); setAttachTo(null); refresh(); toast.success("Domain mapping created"); }}
      />
    </div>
  );
}

function AccountModal({
  open,
  account,
  onClose,
  onSaved,
}: {
  open: boolean;
  account: CloudflareAccount | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [accountId, setAccountId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setName(account?.name ?? "");
      setToken("");
      setAccountId(account?.accountId ?? "");
      setError(null);
    }
  }, [open, account]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (account) {
        await api.updateCloudflareAccount(account.id, { name, token: token || undefined, accountId });
      } else {
        await api.createCloudflareAccount({ name, token, accountId });
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
      title={account ? "Edit Cloudflare account" : "Add Cloudflare account"}
      description="Use a scoped token with Zone:Read and DNS:Edit for only the zones you intend to manage."
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" type="submit" form="cloudflare-account-form" loading={busy}>Save account</Button></>}
    >
      <form id="cloudflare-account-form" className="stack" onSubmit={(event) => void submit(event)}>
        {error ? <div className="form-error">{error}</div> : null}
        <Field label="Account label" required>
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="Production zones" required />
        </Field>
        <Field label={account ? "Replace API token (optional)" : "API token"} required={!account} hint="Never logged or returned.">
          <Input type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="new-password" required={!account} />
        </Field>
        <Field label="Cloudflare account ID" hint="Optional; used for display only.">
          <Input value={accountId} onChange={(event) => setAccountId(event.target.value)} className="mono" />
        </Field>
        <div className="token-help">
          <ShieldCheck size={16} />
          <span>Recommended permissions: Zone → Zone → Read, Zone → DNS → Edit. Scope to specific zones.</span>
        </div>
      </form>
    </Modal>
  );
}

function ZonesModal({ account, onClose }: { account: CloudflareAccount | null; onClose: () => void }) {
  const toast = useToast();
  const [zones, setZones] = useState<Zone[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Zone | null>(null);
  const [records, setRecords] = useState<DNSRecord[] | null>(null);

  useEffect(() => {
    if (!account) return;
    setZones(null);
    setSelected(null);
    setRecords(null);
    setError(null);
    setLoading(true);
    api
      .zones(account.id)
      .then((result) => setZones(result.items))
      .catch((err) => setError(errorMessage(err)))
      .finally(() => setLoading(false));
  }, [account]);

  async function loadRecords(zone: Zone) {
    if (!account) return;
    setSelected(zone);
    setRecords(null);
    try {
      const result = await api.zoneRecords(account.id, zone.id);
      setRecords(result.items);
    } catch (err) {
      toast.error("Could not load DNS records", errorMessage(err));
    }
  }

  return (
    <Modal open={Boolean(account)} onClose={onClose} title={`Zones · ${account?.name ?? ""}`} size="xl" footer={<Button onClick={onClose}>Close</Button>}>
      {loading ? <LoadingBlock label="Reading zones from Cloudflare…" /> : error ? <ErrorBanner message={error} /> : !zones?.length ? <EmptyState title="No zones visible" description="The token may be scoped to different zones." /> : (
        <div className="zones-layout">
          <div className="zone-list">
            {zones.map((zone) => (
              <button key={zone.id} className={`zone-item ${selected?.id === zone.id ? "active" : ""}`} onClick={() => void loadRecords(zone)}>
                <Globe2 size={16} />
                <span><strong>{zone.name}</strong><small>{zone.status}</small></span>
              </button>
            ))}
          </div>
          <div className="zone-records">
            {!selected ? <EmptyState title="Select a zone" description="DNS records are read-only here; domain attachment creates owned records." compact /> : !records ? <LoadingBlock label="Loading records…" /> : records.length === 0 ? <EmptyState title="No records" compact /> : (
              <div className="table-wrap">
                <table className="data-table compact">
                  <thead><tr><th>Name</th><th>Type</th><th>Content</th><th>Proxied</th><th>Comment</th></tr></thead>
                  <tbody>
                    {records.map((record) => (
                      <tr key={record.id}>
                        <td className="mono small">{record.name}</td>
                        <td><Badge tone="neutral">{record.type}</Badge></td>
                        <td className="mono small">{record.content}</td>
                        <td>{record.proxied ? <Badge tone="warning">yes</Badge> : <span className="muted">no</span>}</td>
                        <td className="muted small">{record.comment || "—"}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}
    </Modal>
  );
}

function DetachDomainDialog({ domain, onClose, onDone }: { domain: Domain | null; onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const [removeCaddy, setRemoveCaddy] = useState(true);
  const [removeDns, setRemoveDns] = useState(false);
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (domain) {
      setRemoveCaddy(domain.caddyManaged);
      setRemoveDns(false);
      setConfirm("");
    }
  }, [domain]);

  async function submit() {
    if (!domain) return;
    setBusy(true);
    try {
      await api.detachDomain(domain.id, { confirmation: confirm, removeCaddy, removeDns });
      toast.success("Domain removal queued");
      onDone();
    } catch (err) {
      toast.error("Could not remove domain", errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={Boolean(domain)}
      onClose={onClose}
      title="Detach domain"
      size="sm"
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="danger" onClick={() => void submit()} disabled={confirm !== domain?.hostname} loading={busy}>Detach</Button></>}
    >
      {domain ? (
        <div className="stack">
          <p>Remove <strong>{domain.hostname}</strong> from {domain.instanceName}?</p>
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
