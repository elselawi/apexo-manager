import { useEffect, useState } from "react";
import { Archive, Download, KeyRound, Plus, RefreshCw, RotateCcw, Save, Settings, ShieldAlert, Trash2, Users } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useToast } from "../lib/toast";
import { formatBytes, formatRelative } from "../lib/format";
import type { AdminSettings, AdminSuperuser, AuthCollection, AuthRecord, InstanceDetail, NativeBackup } from "../types";
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  Field,
  Input,
  LoadingBlock,
  Modal,
  Notice,
  Select,
  Switch,
  Tabs,
  Textarea,
} from "../components/ui";

export function InstanceAdmin({ data, onRefresh }: { data: InstanceDetail; onRefresh: () => void }) {
  const { instance } = data;
  const [section, setSection] = useState("settings");
  return (
    <div className="detail-grid">
      <div className="detail-main">
        <Notice tone="warning" title="Administrative writes">
          These actions use the linked PocketBase superuser and the documented management APIs. Settings changes,
          native restores, and identity changes are audited. Application collection data is not editable here.
        </Notice>
        <Tabs
          tabs={[
            { id: "settings", label: "Settings" },
            { id: "backups", label: "Native backups" },
            { id: "superusers", label: "Superusers" },
            { id: "users", label: "Auth users" },
          ]}
          active={section}
          onChange={setSection}
        />
        {section === "settings" ? <AdminSettingsCard instanceId={instance.id} instanceName={instance.name} /> : null}
        {section === "backups" ? <AdminBackupsCard instance={instance} /> : null}
        {section === "superusers" ? <AdminSuperusersCard instance={instance} /> : null}
        {section === "users" ? <AdminUsersCard instance={instance} onRefresh={onRefresh} /> : null}
      </div>
    </div>
  );
}

function AdminSettingsCard({ instanceId, instanceName }: { instanceId: string; instanceName: string }) {
  const toast = useToast();
  const [settings, setSettings] = useState<AdminSettings | null>(null);
  const [rulesText, setRulesText] = useState("[]");
  const [proxyText, setProxyText] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmSave, setConfirmSave] = useState(false);

  async function load() {
    setError("");
    try {
      const result = await api.adminSettings(instanceId);
      setSettings(result.settings);
      setRulesText(JSON.stringify(result.settings.rateLimits.rules, null, 2));
      setProxyText(result.settings.trustedProxy.headers.join("\n"));
    } catch (err) {
      setError(errorMessage(err));
    }
  }
  useEffect(() => { void load(); }, [instanceId]);

  async function save() {
    if (!settings) return;
    let rules;
    try {
      rules = JSON.parse(rulesText);
      if (!Array.isArray(rules)) throw new Error("rules must be an array");
    } catch (err) {
      setError(`Rate limit rules: ${errorMessage(err)}`);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const payload: AdminSettings = {
        ...settings,
        rateLimits: { ...settings.rateLimits, rules },
        trustedProxy: {
          ...settings.trustedProxy,
          headers: proxyText.split("\n").map((s) => s.trim()).filter(Boolean),
        },
      };
      const result = await api.updateAdminSettings(instanceId, payload);
      setSettings(result.settings);
      setRulesText(JSON.stringify(result.settings.rateLimits.rules, null, 2));
      setProxyText(result.settings.trustedProxy.headers.join("\n"));
      toast.success("PocketBase settings saved");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  function meta<K extends keyof AdminSettings["meta"]>(key: K, value: AdminSettings["meta"][K]) {
    setSettings((s) => s ? { ...s, meta: { ...s.meta, [key]: value } } : s);
  }
  function smtp<K extends keyof AdminSettings["smtp"]>(key: K, value: AdminSettings["smtp"][K]) {
    setSettings((s) => s ? { ...s, smtp: { ...s.smtp, [key]: value } } : s);
  }
  function s3<K extends keyof AdminSettings["s3"]>(key: K, value: AdminSettings["s3"][K]) {
    setSettings((s) => s ? { ...s, s3: { ...s.s3, [key]: value } } : s);
  }
  function backups<K extends keyof AdminSettings["backups"]>(key: K, value: AdminSettings["backups"][K]) {
    setSettings((s) => s ? { ...s, backups: { ...s.backups, [key]: value } } : s);
  }
  function backupS3<K extends keyof AdminSettings["backups"]["s3"]>(key: K, value: AdminSettings["backups"]["s3"][K]) {
    setSettings((s) => s ? { ...s, backups: { ...s.backups, s3: { ...s.backups.s3, [key]: value } } } : s);
  }
  function rate<K extends keyof AdminSettings["rateLimits"]>(key: K, value: AdminSettings["rateLimits"][K]) {
    setSettings((s) => s ? { ...s, rateLimits: { ...s.rateLimits, [key]: value } } : s);
  }
  function logs<K extends keyof AdminSettings["logs"]>(key: K, value: AdminSettings["logs"][K]) {
    setSettings((s) => s ? { ...s, logs: { ...s.logs, [key]: value } } : s);
  }
  function batch<K extends keyof AdminSettings["batch"]>(key: K, value: AdminSettings["batch"][K]) {
    setSettings((s) => s ? { ...s, batch: { ...s.batch, [key]: value } } : s);
  }

  if (error && !settings) return <Card title="PocketBase settings"><div className="form-error">{error}</div><Button onClick={() => void load()}>Retry</Button></Card>;
  if (!settings) return <Card title="PocketBase settings"><LoadingBlock label="Reading PocketBase settings…" /></Card>;

  return (
    <Card
      title="PocketBase settings"
      subtitle="Typed, partial updates. Secrets are only replaced when a new value is entered."
      actions={<Button size="sm" variant="secondary" icon={<RefreshCw size={14} />} onClick={() => void load()}>Reload</Button>}
    >
      {error ? <div className="form-error">{error}</div> : null}
      <div className="form-grid">
        <div className="form-section-title form-span"><h3>Application</h3><p>Name, public URL, sender identity.</p></div>
        <Field label="Application name"><Input value={settings.meta.appName} onChange={(e) => meta("appName", e.target.value)} /></Field>
        <Field label="Application URL"><Input value={settings.meta.appURL} onChange={(e) => meta("appURL", e.target.value)} className="mono" /></Field>
        <Field label="Sender name"><Input value={settings.meta.senderName} onChange={(e) => meta("senderName", e.target.value)} /></Field>
        <Field label="Sender address"><Input type="email" value={settings.meta.senderAddress} onChange={(e) => meta("senderAddress", e.target.value)} /></Field>
        <div className="form-span"><Switch checked={settings.meta.hideControls} onChange={(v) => meta("hideControls", v)} label="Hide controls" description="Hides the PocketBase dashboard controls for non-superusers." /></div>

        <div className="form-section-title form-span"><h3>Mail / SMTP</h3><p>Password is never returned; leave blank to preserve it.</p></div>
        <div className="form-span"><Switch checked={settings.smtp.enabled} onChange={(v) => smtp("enabled", v)} label="Enable SMTP" /></div>
        <Field label="Host"><Input value={settings.smtp.host} onChange={(e) => smtp("host", e.target.value)} /></Field>
        <Field label="Port"><Input type="number" value={settings.smtp.port} onChange={(e) => smtp("port", Number(e.target.value))} /></Field>
        <Field label="Username"><Input value={settings.smtp.username} onChange={(e) => smtp("username", e.target.value)} autoComplete="username" /></Field>
        <Field label="Password (blank preserves)"><Input type="password" value={settings.smtp.password ?? ""} onChange={(e) => smtp("password", e.target.value)} autoComplete="new-password" /></Field>
        <Field label="Auth method"><Select value={settings.smtp.authMethod} onChange={(e) => smtp("authMethod", e.target.value)}><option value="">default</option><option value="PLAIN">PLAIN</option><option value="LOGIN">LOGIN</option></Select></Field>
        <Field label="Local name"><Input value={settings.smtp.localName} onChange={(e) => smtp("localName", e.target.value)} /></Field>
        <div className="form-span"><Switch checked={settings.smtp.tls} onChange={(v) => smtp("tls", v)} label="Implicit TLS" description="Use port 465 with implicit TLS; otherwise STARTTLS is used." /></div>

        <div className="form-section-title form-span"><h3>S3 storage</h3><p>Application file storage. A blank secret preserves the current one.</p></div>
        <div className="form-span"><Switch checked={settings.s3.enabled} onChange={(v) => s3("enabled", v)} label="Enable S3 storage" /></div>
        <Field label="Endpoint"><Input value={settings.s3.endpoint} onChange={(e) => s3("endpoint", e.target.value)} className="mono" /></Field>
        <Field label="Region"><Input value={settings.s3.region} onChange={(e) => s3("region", e.target.value)} /></Field>
        <Field label="Bucket"><Input value={settings.s3.bucket} onChange={(e) => s3("bucket", e.target.value)} /></Field>
        <Field label="Access key"><Input value={settings.s3.accessKey} onChange={(e) => s3("accessKey", e.target.value)} className="mono" /></Field>
        <Field label="Secret key (blank preserves)"><Input type="password" value={settings.s3.secret ?? ""} onChange={(e) => s3("secret", e.target.value)} autoComplete="new-password" /></Field>
        <div className="form-span"><Switch checked={settings.s3.forcePathStyle} onChange={(v) => s3("forcePathStyle", v)} label="Force path-style addressing" /></div>

        <div className="form-section-title form-span"><h3>Backups</h3><p>PocketBase native backup schedule and retention.</p></div>
        <Field label="Cron (UTC)"><Input value={settings.backups.cron} onChange={(e) => backups("cron", e.target.value)} className="mono" /></Field>
        <Field label="Max keep"><Input type="number" value={settings.backups.cronMaxKeep} onChange={(e) => backups("cronMaxKeep", Number(e.target.value))} /></Field>
        <div className="form-section-title form-span"><h3>Backup S3 destination</h3><p>Optional separate storage for PocketBase backup files.</p></div>
        <div className="form-span"><Switch checked={settings.backups.s3.enabled} onChange={(v) => backupS3("enabled", v)} label="Enable backup S3 storage" /></div>
        <Field label="Endpoint"><Input value={settings.backups.s3.endpoint} onChange={(e) => backupS3("endpoint", e.target.value)} className="mono" /></Field>
        <Field label="Region"><Input value={settings.backups.s3.region} onChange={(e) => backupS3("region", e.target.value)} /></Field>
        <Field label="Bucket"><Input value={settings.backups.s3.bucket} onChange={(e) => backupS3("bucket", e.target.value)} /></Field>
        <Field label="Access key"><Input value={settings.backups.s3.accessKey} onChange={(e) => backupS3("accessKey", e.target.value)} className="mono" /></Field>
        <Field label="Secret key (blank preserves)"><Input type="password" value={settings.backups.s3.secret ?? ""} onChange={(e) => backupS3("secret", e.target.value)} autoComplete="new-password" /></Field>
        <div className="form-span"><Switch checked={settings.backups.s3.forcePathStyle} onChange={(v) => backupS3("forcePathStyle", v)} label="Force path-style addressing" /></div>

        <div className="form-section-title form-span"><h3>Rate limits</h3><p>JSON array of documented rules. Invalid rules are rejected.</p></div>
        <div className="form-span"><Switch checked={settings.rateLimits.enabled} onChange={(v) => rate("enabled", v)} label="Enable rate limits" /></div>
        <Field label="Rules (JSON)"><Textarea value={rulesText} onChange={(e) => setRulesText(e.target.value)} rows={8} className="mono" /></Field>

        <div className="form-section-title form-span"><h3>Trusted proxies</h3><p>One header per line.</p></div>
        <Field label="Headers"><Textarea value={proxyText} onChange={(e) => setProxyText(e.target.value)} rows={3} className="mono" /></Field>
        <div className="form-span"><Switch checked={settings.trustedProxy.useLeftmostIP} onChange={(v) => setSettings((s) => s ? { ...s, trustedProxy: { ...s.trustedProxy, useLeftmostIP: v } } : s)} label="Use leftmost IP" /></div>

        <div className="form-section-title form-span"><h3>Logs and batch</h3><p>Retention and batch endpoint policy.</p></div>
        <Field label="Log retention (days)"><Input type="number" value={settings.logs.maxDays} onChange={(e) => logs("maxDays", Number(e.target.value))} /></Field>
        <Field label="Minimum log level"><Input type="number" value={settings.logs.minLevel} onChange={(e) => logs("minLevel", Number(e.target.value))} /></Field>
        <div className="form-span"><Switch checked={settings.logs.logIP} onChange={(v) => logs("logIP", v)} label="Log IP addresses" /></div>
        <div className="form-span"><Switch checked={settings.logs.logAuthId} onChange={(v) => logs("logAuthId", v)} label="Log auth record IDs" /></div>
        <div className="form-span"><Switch checked={settings.batch.enabled} onChange={(v) => batch("enabled", v)} label="Enable batch API" /></div>
        <Field label="Batch max requests"><Input type="number" value={settings.batch.maxRequests} onChange={(e) => batch("maxRequests", Number(e.target.value))} /></Field>
        <Field label="Batch timeout (seconds)"><Input type="number" value={settings.batch.timeout} onChange={(e) => batch("timeout", Number(e.target.value))} /></Field>
        <Field label="Batch max body size"><Input type="number" value={settings.batch.maxBodySize} onChange={(e) => batch("maxBodySize", Number(e.target.value))} /></Field>

        <div className="form-span button-row admin-save-bar">
          <Button variant="primary" icon={<Save size={15} />} onClick={() => setConfirmSave(true)} loading={saving}>Save PocketBase settings</Button>
        </div>
      </div>
      <ConfirmDialog
        open={confirmSave}
        onClose={() => setConfirmSave(false)}
        onConfirm={() => { setConfirmSave(false); void save(); }}
        title="Apply PocketBase settings"
        message={<p>Apply these settings through PocketBase&apos;s documented settings API? Rate limits, trusted proxies, SMTP, and backup changes can affect access or scheduled backups.</p>}
        confirmLabel="Apply settings"
        requireText={instanceName}
        danger
        busy={saving}
      />
    </Card>
  );
}

function AdminBackupsCard({ instance }: { instance: InstanceDetail["instance"] }) {
  const toast = useToast();
  const [items, setItems] = useState<NativeBackup[] | null>(null);
  const [creating, setCreating] = useState(false);
  const [confirm, setConfirm] = useState<{ action: "delete" | "restore"; key: string } | null>(null);
  const [busy, setBusy] = useState(false);

  async function load() {
    try { setItems((await api.adminBackups(instance.id)).items); } catch (err) { toast.error("Could not load native backups", errorMessage(err)); setItems([]); }
  }
  useEffect(() => { void load(); }, [instance.id]);

  async function create() {
    setCreating(true);
    try { await api.createAdminBackup(instance.id); toast.success("PocketBase backup created"); await load(); }
    catch (err) { toast.error("Backup creation failed", errorMessage(err)); }
    finally { setCreating(false); }
  }
  async function runConfirm() {
    if (!confirm) return;
    setBusy(true);
    try {
      if (confirm.action === "delete") await api.deleteAdminBackup(instance.id, confirm.key);
      else await api.restoreAdminBackup(instance.id, confirm.key);
      toast.success(confirm.action === "delete" ? "Native backup deleted" : "Native backup restored");
      setConfirm(null);
      await load();
    } catch (err) { toast.error("Backup action failed", errorMessage(err)); }
    finally { setBusy(false); }
  }

  return (
    <Card
      title="Native PocketBase backups"
      subtitle="Files in the instance data directory. Separate from pbmanager backup records."
      actions={<div className="button-row"><Button size="sm" variant="secondary" icon={<RefreshCw size={14} />} onClick={() => void load()}>Refresh</Button><Button size="sm" variant="primary" icon={<Archive size={14} />} onClick={() => void create()} loading={creating}>Create native backup</Button></div>}
      padded={false}
    >
      {items === null ? <div style={{ padding: 16 }}><LoadingBlock label="Reading native backups…" /></div> : items.length === 0 ? (
        <div style={{ padding: 16 }} className="muted">No native PocketBase backups.</div>
      ) : (
        <div className="table-wrap">
          <table className="data-table compact">
            <thead><tr><th>Backup</th><th>Size</th><th>Modified</th><th /></tr></thead>
            <tbody>{items.map((b) => (
              <tr key={b.key}>
                <td className="mono small">{b.key}</td>
                <td>{formatBytes(b.size)}</td>
                <td className="muted">{formatRelative(b.modified)}</td>
                <td className="table-actions">
                  <a className="button button-ghost button-sm" href={api.adminBackupDownloadUrl(instance.id, b.key)}><Download size={14} />Download</a>
                  <Button size="sm" variant="ghost" icon={<RotateCcw size={14} />} onClick={() => setConfirm({ action: "restore", key: b.key })}>Restore</Button>
                  <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={() => setConfirm({ action: "delete", key: b.key })}>Delete</Button>
                </td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      )}
      <ConfirmDialog
        open={Boolean(confirm)}
        onClose={() => setConfirm(null)}
        onConfirm={() => void runConfirm()}
        title={confirm?.action === "restore" ? "Restore native PocketBase backup" : "Delete native PocketBase backup"}
        message={<p>{confirm?.action === "restore" ? "PocketBase will restore this backup and restart. Current data is replaced by the backup contents." : "This removes the native backup file from the instance."} <strong>{confirm?.key}</strong></p>}
        confirmLabel={confirm?.action === "restore" ? "Restore backup" : "Delete backup"}
        requireText={instance.name}
        danger
        busy={busy}
      />
    </Card>
  );
}

function AdminSuperusersCard({ instance }: { instance: InstanceDetail["instance"] }) {
  const toast = useToast();
  const [items, setItems] = useState<AdminSuperuser[] | null>(null);
  const [modal, setModal] = useState<{ mode: "create" } | { mode: "edit"; record: AdminSuperuser } | null>(null);
  const [deleting, setDeleting] = useState<AdminSuperuser | null>(null);

  async function load() {
    try { setItems((await api.adminSuperusers(instance.id)).items); }
    catch (err) { toast.error("Could not load superusers", errorMessage(err)); setItems([]); }
  }
  useEffect(() => { void load(); }, [instance.id]);

  return (
    <Card
      title="Superusers"
      subtitle="PocketBase superusers can do anything. Changes use the official CLI and briefly stop the instance."
      actions={<div className="button-row"><Button size="sm" variant="secondary" icon={<RefreshCw size={14} />} onClick={() => void load()}>Refresh</Button><Button size="sm" variant="primary" icon={<Plus size={14} />} onClick={() => setModal({ mode: "create" })}>New superuser</Button></div>}
      padded={false}
    >
      {items === null ? <div style={{ padding: 16 }}><LoadingBlock label="Reading superusers…" /></div> : (
        <div className="table-wrap"><table className="data-table compact">
          <thead><tr><th>Email</th><th>Verified</th><th>Created</th><th /></tr></thead>
          <tbody>{items.map((u) => {
            const managerOwned = instance.pbSuperuserManaged && u.email === instance.pbSuperuserEmail;
            return (
              <tr key={u.id}>
                <td><strong>{u.email}</strong>{managerOwned ? <div><Badge tone="success">managed by pbmanager</Badge></div> : null}</td>
                <td>{u.verified ? <Badge tone="success">verified</Badge> : <Badge tone="warning">unverified</Badge>}</td>
                <td className="muted">{formatRelative(u.created)}</td>
                <td className="table-actions">
                  <Button size="sm" variant="ghost" icon={<KeyRound size={14} />} onClick={() => setModal({ mode: "edit", record: u })}>Edit</Button>
                  <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={() => setDeleting(u)}>Delete</Button>
                </td>
              </tr>
            );
          })}</tbody>
        </table></div>
      )}
      <SuperuserModal instance={instance} modal={modal} onClose={() => setModal(null)} onSaved={() => { setModal(null); void load(); }} />
      <ConfirmDialog
        open={Boolean(deleting)}
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          if (!deleting) return;
          void api.deleteAdminSuperuser(instance.id, deleting.id, deleting.email)
            .then(() => { toast.success("Superuser deleted"); setDeleting(null); void load(); })
            .catch((err) => toast.error("Could not delete superuser", errorMessage(err)));
        }}
        title="Delete PocketBase superuser"
        message={<p>Delete <strong>{deleting?.email}</strong>? The last superuser is protected. The instance briefly stops.</p>}
        confirmLabel="Delete superuser"
        requireText={instance.name}
        danger
      />
    </Card>
  );
}

function SuperuserModal({ instance, modal, onClose, onSaved }: {
  instance: InstanceDetail["instance"];
  modal: { mode: "create" } | { mode: "edit"; record: AdminSuperuser } | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!modal) return;
    setError(""); setPassword("");
    setEmail(modal.mode === "edit" ? modal.record.email : "");
  }, [modal]);

  const managerOwned = Boolean(modal?.mode === "edit" && instance.pbSuperuserManaged && modal.record.email === instance.pbSuperuserEmail);

  async function submit() {
    if (!modal) return;
    setBusy(true); setError("");
    try {
      if (modal.mode === "create") await api.createAdminSuperuser(instance.id, email, password);
      else await api.updateAdminSuperuser(instance.id, modal.record.id, email, password);
      toast.success(modal.mode === "create" ? "Superuser created" : "Superuser updated");
      onSaved();
    } catch (err) { setError(errorMessage(err)); }
    finally { setBusy(false); }
  }

  return (
    <Modal
      open={Boolean(modal)}
      onClose={onClose}
      title={modal?.mode === "create" ? "New PocketBase superuser" : "Edit PocketBase superuser"}
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" onClick={() => void submit()} loading={busy}>Save superuser</Button></>}
    >
      <div className="form-grid">
        {error ? <div className="form-error form-span">{error}</div> : null}
        {managerOwned ? <div className="form-span"><Notice tone="warning">This is the pbmanager-managed account. The watchdog will detect and repair a changed password or deleted account.</Notice></div> : null}
        <Field label="Email" required><Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></Field>
        <Field label={modal?.mode === "edit" ? "New password (required)" : "Password"} required><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required /></Field>
        <div className="form-span"><Notice tone="info">Superuser changes use the official CLI and stop the service briefly. The last superuser cannot be deleted.</Notice></div>
      </div>
    </Modal>
  );
}

function AdminUsersCard({ instance, onRefresh }: { instance: InstanceDetail["instance"]; onRefresh: () => void }) {
  const toast = useToast();
  const [collections, setCollections] = useState<AuthCollection[]>([]);
  const [collectionId, setCollectionId] = useState("");
  const [users, setUsers] = useState<AuthRecord[] | null>(null);
  const [modal, setModal] = useState<{ mode: "create" } | { mode: "edit"; record: AuthRecord } | null>(null);
  const [deleting, setDeleting] = useState<AuthRecord | null>(null);

  useEffect(() => {
    void api.authCollections(instance.id).then((r) => {
      setCollections(r.items);
      if (r.items[0]) setCollectionId((current) => current || r.items[0].id);
    }).catch((err) => toast.error("Could not load auth collections", errorMessage(err)));
  }, [instance.id]);
  useEffect(() => {
    if (!collectionId) { setUsers([]); return; }
    void api.authUsers(instance.id, collectionId).then((r) => setUsers(r.items))
      .catch((err) => { toast.error("Could not load auth users", errorMessage(err)); setUsers([]); });
  }, [instance.id, collectionId]);

  async function load() {
    if (!collectionId) return;
    try { setUsers((await api.authUsers(instance.id, collectionId)).items); }
    catch (err) { toast.error("Could not load auth users", errorMessage(err)); }
  }

  return (
    <Card
      title="Auth users"
      subtitle="Only auth collections are exposed; application data collections are not editable."
      actions={<div className="button-row"><Select value={collectionId} onChange={(e) => setCollectionId(e.target.value)} className="compact-select">{collections.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</Select><Button size="sm" variant="secondary" icon={<RefreshCw size={14} />} onClick={() => void load()}>Refresh</Button><Button size="sm" variant="primary" icon={<Users size={14} />} onClick={() => setModal({ mode: "create" })} disabled={!collectionId}>New user</Button></div>}
      padded={false}
    >
      {!collectionId ? <div style={{ padding: 16 }} className="muted">No auth collections found.</div> : users === null ? <div style={{ padding: 16 }}><LoadingBlock label="Reading auth users…" /></div> : users.length === 0 ? <div style={{ padding: 16 }} className="muted">No users in this collection.</div> : (
        <div className="table-wrap"><table className="data-table compact">
          <thead><tr><th>Email</th><th>Username</th><th>Name</th><th>Verified</th><th>Created</th><th /></tr></thead>
          <tbody>{users.map((u) => (
            <tr key={u.id}>
              <td>{String(u.email ?? "—")}</td>
              <td>{String(u.username ?? "—")}</td>
              <td>{String(u.name ?? "—")}</td>
              <td>{u.verified ? <Badge tone="success">verified</Badge> : <Badge tone="warning">unverified</Badge>}</td>
              <td className="muted">{formatRelative(String(u.created ?? ""))}</td>
              <td className="table-actions">
                <Button size="sm" variant="ghost" icon={<KeyRound size={14} />} onClick={() => setModal({ mode: "edit", record: u })}>Edit</Button>
                <Button size="sm" variant="ghost" icon={<Trash2 size={14} />} onClick={() => setDeleting(u)}>Delete</Button>
              </td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <AuthUserModal instance={instance} collectionId={collectionId} modal={modal} onClose={() => setModal(null)} onSaved={() => { setModal(null); void load(); onRefresh(); }} />
      <ConfirmDialog
        open={Boolean(deleting)}
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          if (!deleting) return;
          void api.deleteAuthUser(instance.id, collectionId, deleting.id, String(deleting.email ?? deleting.id))
            .then(() => { toast.success("Auth user deleted"); setDeleting(null); void load(); })
            .catch((err) => toast.error("Could not delete user", errorMessage(err)));
        }}
        title="Delete auth user"
        message={<p>Delete <strong>{String(deleting?.email ?? deleting?.id ?? "")}</strong>? This removes the auth record.</p>}
        confirmLabel="Delete user"
        requireText={instance.name}
        danger
      />
    </Card>
  );
}

function AuthUserModal({ instance, collectionId, modal, onClose, onSaved }: {
  instance: InstanceDetail["instance"];
  collectionId: string;
  modal: { mode: "create" } | { mode: "edit"; record: AuthRecord } | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [verified, setVerified] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!modal) return;
    setError(""); setPassword("");
    const r = modal.mode === "edit" ? modal.record : null;
    setEmail(String(r?.email ?? "")); setUsername(String(r?.username ?? "")); setName(String(r?.name ?? "")); setVerified(Boolean(r?.verified));
  }, [modal]);

  async function submit() {
    if (!modal || !collectionId) return;
    setBusy(true); setError("");
    const data: Record<string, unknown> = { email, username, name, verified };
    if (password) data.password = password;
    try {
      if (modal.mode === "create") await api.createAuthUser(instance.id, collectionId, data);
      else await api.updateAuthUser(instance.id, collectionId, modal.record.id, data);
      toast.success(modal.mode === "create" ? "Auth user created" : "Auth user updated");
      onSaved();
    } catch (err) { setError(errorMessage(err)); }
    finally { setBusy(false); }
  }

  return (
    <Modal
      open={Boolean(modal)}
      onClose={onClose}
      title={modal?.mode === "create" ? "New auth user" : "Edit auth user"}
      footer={<><Button onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" onClick={() => void submit()} loading={busy}>Save user</Button></>}
    >
      <div className="form-grid">
        {error ? <div className="form-error form-span">{error}</div> : null}
        <Field label="Email"><Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></Field>
        <Field label="Username"><Input value={username} onChange={(e) => setUsername(e.target.value)} /></Field>
        <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field>
        <Field label={modal?.mode === "edit" ? "New password (blank preserves)" : "Password"} required={modal?.mode === "create"}><Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required={modal?.mode === "create"} /></Field>
        <div className="form-span"><Switch checked={verified} onChange={setVerified} label="Email verified" /></div>
        <div className="form-span"><Notice tone="info">Only standard auth fields are exposed. Application data and custom collection records are intentionally excluded.</Notice></div>
      </div>
    </Modal>
  );
}
