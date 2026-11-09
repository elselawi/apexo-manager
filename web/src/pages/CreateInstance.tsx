import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  CircleAlert,
  Cloud,
  Database,
  Globe2,
  HardDrive,
  Network,
  ServerCog,
  ShieldCheck,
} from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useDocumentTitle } from "../lib/hooks";
import { useToast } from "../lib/toast";
import type {
  CloudflareAccount,
  InstanceCreateInput,
  Job,
  SMTPProfile,
  StorageProfile,
  SettingsView,
} from "../types";
import {
  Badge,
  Button,
  Card,
  ErrorBanner,
  Field,
  Input,
  PageHeader,
  Select,
  Switch,
  Textarea,
} from "../components/ui";
import { JobProgress } from "../components/JobProgress";

interface FormState {
  name: string;
  hostname: string;
  pbVersion: string;
  arch: string;
  port: string;
  domain: string;
  baseDir: string;
  dataDir: string;
  binaryPath: string;
  serviceName: string;
  serviceFile: string;
  runAsUser: string;
  createSystemUser: boolean;
  allowRootUser: boolean;
  manageCaddy: boolean;
  manageDns: boolean;
  proxied: boolean;
  cloudflareAccountId: string;
  smtpProfileId: string;
  storageProfileId: string;
  backupEnabled: boolean;
  backupCron: string;
  backupRetention: string;
  notes: string;
  checksum: string;
}

const initialForm: FormState = {
  name: "",
  hostname: "",
  pbVersion: "",
  arch: "",
  port: "",
  domain: "",
  baseDir: "",
  dataDir: "",
  binaryPath: "",
  serviceName: "",
  serviceFile: "",
  runAsUser: "",
  createSystemUser: true,
  allowRootUser: false,
  manageCaddy: true,
  manageDns: false,
  proxied: true,
  cloudflareAccountId: "",
  smtpProfileId: "",
  storageProfileId: "",
  backupEnabled: true,
  backupCron: "0 3 * * *",
  backupRetention: "7",
  notes: "",
  checksum: "",
};

export function CreateInstancePage() {
  useDocumentTitle("Provision instance");
  const navigate = useNavigate();
  const toast = useToast();
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<FormState>(initialForm);
  const [defaultsLoading, setDefaultsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [instanceId, setInstanceId] = useState<string | null>(null);
  const [accounts, setAccounts] = useState<CloudflareAccount[]>([]);
  const [smtpProfiles, setSmtpProfiles] = useState<SMTPProfile[]>([]);
  const [storageProfiles, setStorageProfiles] = useState<StorageProfile[]>([]);
  const [settings, setSettings] = useState<SettingsView | null>(null);

  useEffect(() => {
    void Promise.all([
      api.cloudflareAccounts().then((r) => setAccounts(r.items)).catch(() => undefined),
      api.smtpProfiles().then((r) => setSmtpProfiles(r.items)).catch(() => undefined),
      api.storageProfiles().then((r) => setStorageProfiles(r.items)).catch(() => undefined),
      api.settings().then(setSettings).catch(() => undefined),
    ]);
  }, []);

  function update<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  async function loadDefaults(force = false) {
    if (!form.name.trim()) return;
    setDefaultsLoading(true);
    setError(null);
    try {
      const defaults = await api.instanceDefaults(form.name.trim());
      setForm((current) => ({
        ...current,
        baseDir: force || !current.baseDir ? defaults.baseDir : current.baseDir,
        dataDir: force || !current.dataDir ? defaults.dataDir : current.dataDir,
        binaryPath: force || !current.binaryPath ? defaults.binaryPath : current.binaryPath,
        serviceName: force || !current.serviceName ? defaults.serviceName : current.serviceName,
        serviceFile: force || !current.serviceFile ? defaults.serviceFile : current.serviceFile,
        runAsUser: force || !current.runAsUser ? defaults.runAsUser : current.runAsUser,
        port: force || !current.port ? String(defaults.port) : current.port,
        pbVersion: force || !current.pbVersion ? defaults.pbVersion : current.pbVersion,
        arch: force || !current.arch ? defaults.arch : current.arch,
        backupRetention: force || !current.backupRetention ? String(defaults.backupRetention) : current.backupRetention,
      }));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setDefaultsLoading(false);
    }
  }

  async function next() {
    if (step === 0) {
      if (!form.name.trim()) {
        setError("Instance name is required.");
        return;
      }
      if (!form.hostname) update("hostname", form.name.trim().toLowerCase());
      await loadDefaults(false);
    }
    if (step === 1 && (!form.baseDir || !form.dataDir || !form.serviceFile || !form.port)) {
      setError("Installation, data, service, and port fields are required.");
      return;
    }
    setError(null);
    setStep((value) => Math.min(3, value + 1));
  }

  async function submit() {
    setSubmitting(true);
    setError(null);
    try {
      const input: InstanceCreateInput = {
        name: form.name.trim(),
        hostname: (form.hostname || form.name).trim(),
        pbVersion: form.pbVersion,
        arch: form.arch,
        port: Number(form.port),
        baseDir: form.baseDir,
        dataDir: form.dataDir,
        binaryPath: form.binaryPath,
        serviceName: form.serviceName,
        serviceFile: form.serviceFile,
        runAsUser: form.runAsUser,
        createSystemUser: form.createSystemUser,
        allowRootUser: form.allowRootUser,
        domain: form.domain.trim(),
        cloudflareAccountId: form.cloudflareAccountId,
        manageCaddy: form.manageCaddy && Boolean(form.domain.trim()),
        manageDns: form.manageDns && Boolean(form.domain.trim()),
        proxied: form.proxied,
        smtpProfileId: form.smtpProfileId,
        storageProfileId: form.storageProfileId,
        backupEnabled: form.backupEnabled,
        backupCron: form.backupCron,
        backupRetention: Number(form.backupRetention),
        notes: form.notes,
        checksum: form.checksum,
      };
      const result = await api.createInstance(input);
      setInstanceId(result.instance.id);
      setJob(result.job);
      toast.success("Provisioning queued", `Job ${result.job.id}`);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  const steps = ["Basics", "Paths & service", "Integrations", "Review"];

  if (job) {
    return (
      <div className="page">
        <PageHeader
          eyebrow="Provisioning"
          title={`Provisioning ${form.name}`}
          description="The job is persisted in SQLite and continues even if this browser is closed."
          actions={
            instanceId ? (
              <Button onClick={() => navigate(`/instances/${instanceId}`)} icon={<ArrowRight size={16} />}>
                Open instance
              </Button>
            ) : null
          }
        />
        <JobProgress
          jobId={job.id}
          onFinished={(finished) => {
            if (finished.status === "succeeded") {
              toast.success("Instance provisioned", "Application health checks passed.");
            } else if (finished.status === "failed") {
              toast.error("Provisioning failed", finished.lastError);
            }
          }}
        />
        <Card title="What happens next" subtitle="The manager reports every step and supports safe retries.">
          <ol className="process-list">
            <li>Download the requested PocketBase release and verify its SHA-256 checksum.</li>
            <li>Create directories and a dedicated system user; never overwrite unmanaged data.</li>
            <li>Install the binary, write a hardened systemd unit, enable it, and start the service.</li>
            <li>Optionally create a validated Caddy route and an owned Cloudflare DNS record.</li>
            <li>Verify local health and, if configured, public DNS and HTTPS availability.</li>
          </ol>
          <div className="button-row">
            <Button onClick={() => navigate("/instances/new")}>Provision another</Button>
            <Link to="/jobs" className="button button-secondary button-md">Open job queue</Link>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="page">
      <PageHeader
        eyebrow="PocketBase"
        title="Provision a new instance"
        description="Create an isolated, hardened PocketBase installation with optional managed DNS and Caddy routing."
        actions={<Button onClick={() => navigate("/instances")} icon={<ArrowLeft size={16} />}>Back to instances</Button>}
      />
      <div className="wizard">
        <div className="wizard-steps">
          {steps.map((label, index) => (
            <button
              key={label}
              className={`wizard-step ${index === step ? "active" : ""} ${index < step ? "complete" : ""}`}
              onClick={() => index < step && setStep(index)}
              type="button"
              disabled={index > step}
            >
              <span className="wizard-number">{index < step ? <Check size={14} /> : index + 1}</span>
              <span>{label}</span>
            </button>
          ))}
        </div>
        {error ? <ErrorBanner message={error} /> : null}

        <Card>
          {step === 0 ? (
            <div className="form-grid">
              <div className="form-section-title form-span">
                <h3>Identity and version</h3>
                <p>The instance identifier is used for the directory, service, and user names.</p>
              </div>
              <Field label="Instance name" required hint="Lowercase letters, digits, dots, underscores, or hyphens.">
                <Input value={form.name} onChange={(e) => update("name", e.target.value)} placeholder="clinic-a" autoFocus />
              </Field>
              <Field label="Hostname identifier" required hint="Used for the systemd service; defaults to the instance name.">
                <Input value={form.hostname} onChange={(e) => update("hostname", e.target.value)} placeholder="clinic-a" />
              </Field>
              <Field label="PocketBase version" required hint={settings ? `Server default: ${settings.config.defaultPbVersion}` : undefined}>
                <Input value={form.pbVersion} onChange={(e) => update("pbVersion", e.target.value)} placeholder="0.23.6" />
              </Field>
              <Field label="Architecture">
                <Select value={form.arch} onChange={(e) => update("arch", e.target.value)}>
                  <option value="">Detect from server</option>
                  <option value="amd64">linux amd64</option>
                  <option value="arm64">linux arm64</option>
                  <option value="armv7">linux armv7</option>
                </Select>
              </Field>
              <Field label="Public domain" hint="Optional. Example: clinic-a.example.com">
                <Input value={form.domain} onChange={(e) => update("domain", e.target.value)} placeholder="clinic-a.example.com" />
              </Field>
              <Field label="Notes">
                <Input value={form.notes} onChange={(e) => update("notes", e.target.value)} placeholder="Tenant, owner, or purpose" />
              </Field>
            </div>
          ) : null}

          {step === 1 ? (
            <div className="form-grid">
              <div className="form-section-title form-span">
                <h3>Paths and systemd</h3>
                <p>These defaults are calculated from server settings and can be overridden per instance.</p>
              </div>
              <Field label="Base directory" required>
                <Input value={form.baseDir} onChange={(e) => update("baseDir", e.target.value)} className="mono" />
              </Field>
              <Field label="Data directory" required>
                <Input value={form.dataDir} onChange={(e) => update("dataDir", e.target.value)} className="mono" />
              </Field>
              <Field label="Binary path">
                <Input value={form.binaryPath} onChange={(e) => update("binaryPath", e.target.value)} className="mono" />
              </Field>
              <Field label="Port" required hint="PocketBase should listen on loopback behind Caddy.">
                <Input type="number" value={form.port} onChange={(e) => update("port", e.target.value)} />
              </Field>
              <Field label="Service name">
                <Input value={form.serviceName} onChange={(e) => update("serviceName", e.target.value)} />
              </Field>
              <Field label="Service file">
                <Input value={form.serviceFile} onChange={(e) => update("serviceFile", e.target.value)} className="mono" />
              </Field>
              <Field label="Run-as user" hint="Dedicated unprivileged user for the PocketBase process.">
                <Input value={form.runAsUser} onChange={(e) => update("runAsUser", e.target.value)} />
              </Field>
              <Field label="Expected archive SHA-256" hint="Optional for custom mirrors; required release checksums are still verified when available.">
                <Input value={form.checksum} onChange={(e) => update("checksum", e.target.value)} className="mono" />
              </Field>
              <div className="form-span switch-stack">
                <Switch
                  checked={form.createSystemUser}
                  onChange={(value) => update("createSystemUser", value)}
                  label="Create dedicated system user"
                  description="Recommended. The manager creates the user with a non-login shell."
                />
                <Switch
                  checked={form.allowRootUser}
                  onChange={(value) => update("allowRootUser", value)}
                  label="Allow running as root"
                  description="Only enable if you explicitly accept the security tradeoff."
                />
              </div>
              {defaultsLoading ? <div className="muted form-span">Loading calculated defaults…</div> : null}
              <div className="form-span">
                <Button size="sm" variant="ghost" onClick={() => void loadDefaults(true)}>
                  Recalculate server defaults
                </Button>
              </div>
            </div>
          ) : null}

          {step === 2 ? (
            <div className="form-grid">
              <div className="form-section-title form-span">
                <h3>Routing, DNS, and application profiles</h3>
                <p>Integrations are optional. Every managed change is validated and reversible.</p>
              </div>
              <div className="form-span integration-block">
                <div className="integration-heading"><Network size={18} /><div><strong>Caddy route</strong><span>Adds a dedicated snippet; unrelated configuration is preserved.</span></div></div>
                <Switch
                  checked={form.manageCaddy}
                  onChange={(value) => update("manageCaddy", value)}
                  label="Create managed Caddy route"
                  description={form.domain ? `Route ${form.domain} to 127.0.0.1:${form.port || "port"}` : "Requires a public domain."}
                  disabled={!form.domain}
                />
              </div>
              <div className="form-span integration-block">
                <div className="integration-heading"><Globe2 size={18} /><div><strong>Cloudflare DNS</strong><span>Creates or updates an A record owned by the manager.</span></div></div>
                <Switch
                  checked={form.manageDns}
                  onChange={(value) => update("manageDns", value)}
                  label="Manage DNS record"
                  description={accounts.length === 0 ? "Add a Cloudflare account on the Domains page first." : "Conflicting records are never overwritten."}
                  disabled={!form.domain || accounts.length === 0}
                />
                <div className="form-grid nested">
                  <Field label="Cloudflare account">
                    <Select value={form.cloudflareAccountId} onChange={(e) => update("cloudflareAccountId", e.target.value)}>
                      <option value="">Select account</option>
                      {accounts.map((account) => (
                        <option value={account.id} key={account.id}>{account.name} ({account.status})</option>
                      ))}
                    </Select>
                  </Field>
                  <div className="switch-field">
                    <Switch checked={form.proxied} onChange={(value) => update("proxied", value)} label="Cloudflare proxy enabled" />
                  </div>
                </div>
              </div>
              <Field label="SMTP profile" hint="Assignment only. Apply explicitly after provisioning and linking a superuser.">
                <Select value={form.smtpProfileId} onChange={(e) => update("smtpProfileId", e.target.value)}>
                  <option value="">No SMTP profile</option>
                  {smtpProfiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {profile.host}</option>)}
                </Select>
              </Field>
              <Field label="S3 storage profile" hint="Assignment only. Apply explicitly after provisioning.">
                <Select value={form.storageProfileId} onChange={(e) => update("storageProfileId", e.target.value)}>
                  <option value="">No storage profile</option>
                  {storageProfiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {profile.bucket}</option>)}
                </Select>
              </Field>
              <div className="form-span integration-block">
                <div className="integration-heading"><Database size={18} /><div><strong>Backup policy</strong><span>Automatic backups run through the persistent job queue.</span></div></div>
                <Switch
                  checked={form.backupEnabled}
                  onChange={(value) => update("backupEnabled", value)}
                  label="Enable scheduled backups"
                  description="UTC cron expression. Retention applies to verified artifacts."
                />
                <div className="form-grid nested">
                  <Field label="Cron schedule (UTC)">
                    <Input value={form.backupCron} onChange={(e) => update("backupCron", e.target.value)} className="mono" />
                  </Field>
                  <Field label="Retention">
                    <Input type="number" value={form.backupRetention} onChange={(e) => update("backupRetention", e.target.value)} />
                  </Field>
                </div>
              </div>
            </div>
          ) : null}

          {step === 3 ? (
            <div className="review">
              <div className="review-section">
                <h3><Database size={18} /> Instance</h3>
                <dl>
                  <div><dt>Name</dt><dd>{form.name}</dd></div>
                  <div><dt>Hostname</dt><dd>{form.hostname || form.name}</dd></div>
                  <div><dt>Version</dt><dd>{form.pbVersion || "server default"}</dd></div>
                  <div><dt>Port</dt><dd>{form.port}</dd></div>
                  <div><dt>Domain</dt><dd>{form.domain || "none"}</dd></div>
                </dl>
              </div>
              <div className="review-section">
                <h3><ServerCog size={18} /> Paths and service</h3>
                <dl>
                  <div><dt>Base</dt><dd className="mono">{form.baseDir}</dd></div>
                  <div><dt>Data</dt><dd className="mono">{form.dataDir}</dd></div>
                  <div><dt>Binary</dt><dd className="mono">{form.binaryPath}</dd></div>
                  <div><dt>Service</dt><dd className="mono">{form.serviceFile}</dd></div>
                  <div><dt>Run as</dt><dd>{form.runAsUser || "root"}</dd></div>
                </dl>
              </div>
              <div className="review-section">
                <h3><Cloud size={18} /> Integrations</h3>
                <dl>
                  <div><dt>Caddy</dt><dd>{form.domain && form.manageCaddy ? "managed route" : "not managed"}</dd></div>
                  <div><dt>DNS</dt><dd>{form.domain && form.manageDns ? "managed Cloudflare A record" : "not managed"}</dd></div>
                  <div><dt>SMTP</dt><dd>{smtpProfiles.find((p) => p.id === form.smtpProfileId)?.name ?? "none"}</dd></div>
                  <div><dt>Storage</dt><dd>{storageProfiles.find((p) => p.id === form.storageProfileId)?.name ?? "none"}</dd></div>
                  <div><dt>Backups</dt><dd>{form.backupEnabled ? `${form.backupCron} · keep ${form.backupRetention}` : "disabled"}</dd></div>
                </dl>
              </div>
              <div className="review-section safety">
                <h3><ShieldCheck size={18} /> Safety guarantees</h3>
                <ul>
                  <li>Existing unmanaged directories, unit files, and services are never overwritten.</li>
                  <li>Downloads are checksum-verified before installation.</li>
                  <li>Systemd unit is validated with <code>systemd-analyze</code> before installation.</li>
                  <li>Caddy changes are backups-first and validated before reload; failures roll back.</li>
                  <li>Unrelated DNS records are never modified.</li>
                </ul>
              </div>
              {form.manageDns && !form.cloudflareAccountId ? (
                <div className="notice notice-warning"><CircleAlert size={17} /> Select a Cloudflare account before provisioning.</div>
              ) : null}
            </div>
          ) : null}
        </Card>

        <div className="wizard-footer">
          <Button
            variant="ghost"
            onClick={() => (step === 0 ? navigate("/instances") : setStep((value) => value - 1))}
            icon={<ArrowLeft size={16} />}
          >
            {step === 0 ? "Cancel" : "Back"}
          </Button>
          {step < 3 ? (
            <Button variant="primary" onClick={() => void next()} loading={defaultsLoading} icon={<ArrowRight size={16} />}>
              Continue
            </Button>
          ) : (
            <Button
              variant="primary"
              onClick={() => void submit()}
              loading={submitting}
              disabled={form.manageDns && !form.cloudflareAccountId}
              icon={<ServerCog size={16} />}
            >
              Provision instance
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
