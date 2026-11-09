import type {
  Admin,
  AuditEntry,
  AuthStatus,
  Backup,
  CaddyConfigView,
  CloudflareAccount,
  DNSRecord,
  Domain,
  Instance,
  InstanceCreateInput,
  InstanceDefaults,
  InstanceDetail,
  Job,
  MeResponse,
  MonitoringData,
  Overview,
  Page,
  ServerConfig,
  Session,
  SettingsView,
  SMTPProfile,
  StorageProfile,
  Zone,
  Discovered,
} from "../types";

export class ApiError extends Error {
  status: number;
  code: string;
  details?: unknown;
  requestId?: string;

  constructor(status: number, code: string, message: string, details?: unknown, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.requestId = requestId;
  }
}

let csrfToken = "";

export function setCSRFToken(token: string): void {
  csrfToken = token;
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

async function request<T>(method: Method, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers = new Headers({ Accept: "application/json" });
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (csrfToken && method !== "GET") headers.set("X-CSRF-Token", csrfToken);
  const response = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
    credentials: "same-origin",
  });
  const contentType = response.headers.get("content-type") ?? "";
  const payload = contentType.includes("application/json") ? await response.json().catch(() => ({})) : {};
  if (!response.ok) {
    const err = payload?.error ?? {};
    if (response.status === 401) {
      window.dispatchEvent(new CustomEvent("pbmanager:unauthorized"));
    }
    throw new ApiError(
      response.status,
      typeof err.code === "string" ? err.code : "request_failed",
      typeof err.message === "string" ? err.message : `Request failed (${response.status})`,
      err.details,
      payload?.requestId,
    );
  }
  return payload as T;
}

function query(params: Record<string, string | number | boolean | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== "") q.set(key, String(value));
  }
  const encoded = q.toString();
  return encoded ? `?${encoded}` : "";
}

export const api = {
  setCSRF: setCSRFToken,

  authStatus(): Promise<AuthStatus> {
    return request("GET", "/api/v1/auth/status");
  },

  login(username: string, password: string): Promise<{ admin: Admin; csrfToken: string; expiresAt: string }> {
    return request("POST", "/api/v1/auth/login", { username, password });
  },

  me(): Promise<MeResponse> {
    return request("GET", "/api/v1/auth/me");
  },

  logout(): Promise<void> {
    return request("POST", "/api/v1/auth/logout", {});
  },

  changePassword(currentPassword: string, newPassword: string): Promise<{ ok: boolean; message: string }> {
    return request("POST", "/api/v1/auth/change-password", { currentPassword, newPassword });
  },

  ownSessions(): Promise<{ items: Session[]; currentSessionId: string }> {
    return request("GET", "/api/v1/auth/sessions");
  },

  revokeOwnSession(id: string): Promise<{ ok: boolean }> {
    return request("POST", `/api/v1/auth/sessions/${encodeURIComponent(id)}/revoke`, {});
  },

  overview(): Promise<Overview> {
    return request("GET", "/api/v1/overview");
  },

  monitoring(): Promise<MonitoringData> {
    return request("GET", "/api/v1/monitoring");
  },

  instances(params: { q?: string; status?: string; page?: number; perPage?: number } = {}): Promise<Page<Instance>> {
    return request("GET", `/api/v1/instances${query(params)}`);
  },

  instanceDefaults(name: string): Promise<InstanceDefaults> {
    return request("GET", `/api/v1/instances/defaults${query({ name })}`);
  },

  instance(id: string): Promise<InstanceDetail> {
    return request("GET", `/api/v1/instances/${encodeURIComponent(id)}`);
  },

  createInstance(input: InstanceCreateInput): Promise<{ instance: Instance; job: Job }> {
    return request("POST", "/api/v1/instances", input);
  },

  updateInstanceSettings(
    id: string,
    input: Partial<{
      notes: string;
      backupEnabled: boolean;
      backupCron: string;
      backupRetention: number;
      smtpProfileId: string;
      storageProfileId: string;
      cloudflareAccountId: string;
    }>,
  ): Promise<{ instance: Instance }> {
    return request("PATCH", `/api/v1/instances/${encodeURIComponent(id)}`, input);
  },

  deleteInstance(
    id: string,
    input: { confirmation: string; purgeData: boolean; removeCaddy: boolean; removeDns: boolean },
  ): Promise<{ job: Job }> {
    return request("DELETE", `/api/v1/instances/${encodeURIComponent(id)}`, input);
  },

  discover(): Promise<{ items: Discovered[]; warnings: string[] }> {
    return request("GET", "/api/v1/instances/discover");
  },

  importInstance(input: {
    name: string;
    hostname: string;
    baseDir: string;
    binaryPath: string;
    dataDir: string;
    port: number;
    serviceName: string;
    serviceFile: string;
    runAsUser: string;
    pbVersion: string;
    domains: string[];
    notes: string;
    source: string;
  }): Promise<{ instance: Instance }> {
    return request("POST", "/api/v1/instances/import", input);
  },

  instanceAction(id: string, action: "start" | "stop" | "restart" | "sync-caddy" | "apply-smtp" | "apply-storage"): Promise<{ job: Job }> {
    return request("POST", `/api/v1/instances/${encodeURIComponent(id)}/${action}`, {});
  },

  updatePocketBase(id: string, version: string, checksum = ""): Promise<{ job: Job }> {
    return request("POST", `/api/v1/instances/${encodeURIComponent(id)}/update`, { version, checksum });
  },

  verifyInstance(id: string): Promise<Record<string, unknown>> {
    return request("POST", `/api/v1/instances/${encodeURIComponent(id)}/verify`, {});
  },

  instanceLogs(id: string, lines = 200): Promise<{ logs: string; service: string; lines: number }> {
    return request("GET", `/api/v1/instances/${encodeURIComponent(id)}/logs${query({ lines })}`);
  },

  setCredentials(id: string, email: string, password: string): Promise<{ status: string; email: string }> {
    return request("PUT", `/api/v1/instances/${encodeURIComponent(id)}/credentials`, { email, password });
  },

  clearCredentials(id: string): Promise<{ status: string }> {
    return request("DELETE", `/api/v1/instances/${encodeURIComponent(id)}/credentials`);
  },

  testCredentials(id: string): Promise<{ ok: boolean; error?: string }> {
    return request("POST", `/api/v1/instances/${encodeURIComponent(id)}/credentials/test`, {});
  },

  domains(params: { instanceId?: string; q?: string } = {}): Promise<{ items: Domain[]; total: number }> {
    return request("GET", `/api/v1/domains${query(params)}`);
  },

  domain(id: string): Promise<{ domain: Domain }> {
    return request("GET", `/api/v1/domains/${encodeURIComponent(id)}`);
  },

  attachDomain(
    instanceId: string,
    input: { hostname: string; cloudflareAccountId: string; proxied: boolean; manageCaddy: boolean; manageDns: boolean },
  ): Promise<{ domain: Domain; job: Job | null }> {
    return request("POST", `/api/v1/instances/${encodeURIComponent(instanceId)}/domains`, input);
  },

  detachDomain(
    id: string,
    input: { confirmation: string; removeCaddy: boolean; removeDns: boolean },
  ): Promise<{ job: Job }> {
    return request("DELETE", `/api/v1/domains/${encodeURIComponent(id)}`, input);
  },

  checkDomain(id: string): Promise<Record<string, unknown>> {
    return request("POST", `/api/v1/domains/${encodeURIComponent(id)}/check`, {});
  },

  caddy(): Promise<CaddyConfigView> {
    return request("GET", "/api/v1/caddy");
  },

  caddyEnsureImport(): Promise<{ job: Job }> {
    return request("POST", "/api/v1/caddy/ensure-import", {});
  },

  caddyValidate(): Promise<{ valid: boolean; error?: string }> {
    return request("POST", "/api/v1/caddy/validate", {});
  },

  caddyLogs(lines = 200): Promise<{ logs: string; lines: number }> {
    return request("GET", `/api/v1/caddy/logs${query({ lines })}`);
  },

  caddyRemoveRoute(domain: string): Promise<{ removed: boolean; domain: string }> {
    return request("DELETE", `/api/v1/caddy/routes/${encodeURIComponent(domain)}`, {});
  },

  cloudflareAccounts(): Promise<{ items: CloudflareAccount[] }> {
    return request("GET", "/api/v1/cloudflare/accounts");
  },

  createCloudflareAccount(input: { name: string; token: string; accountId: string }): Promise<{ account: CloudflareAccount }> {
    return request("POST", "/api/v1/cloudflare/accounts", input);
  },

  updateCloudflareAccount(
    id: string,
    input: { name: string; token?: string; accountId: string },
  ): Promise<{ account: CloudflareAccount }> {
    return request("PATCH", `/api/v1/cloudflare/accounts/${encodeURIComponent(id)}`, input);
  },

  deleteCloudflareAccount(id: string, confirmation: string): Promise<{ ok: boolean }> {
    return request("DELETE", `/api/v1/cloudflare/accounts/${encodeURIComponent(id)}`, { confirmation });
  },

  validateCloudflareAccount(id: string): Promise<{ ok: boolean; status?: string; error?: string }> {
    return request("POST", `/api/v1/cloudflare/accounts/${encodeURIComponent(id)}/validate`, {});
  },

  zones(accountId: string): Promise<{ items: Zone[] }> {
    return request("GET", `/api/v1/cloudflare/accounts/${encodeURIComponent(accountId)}/zones`);
  },

  zoneRecords(accountId: string, zoneId: string, type = "", name = ""): Promise<{ items: DNSRecord[] }> {
    return request(
      "GET",
      `/api/v1/cloudflare/accounts/${encodeURIComponent(accountId)}/zones/${encodeURIComponent(zoneId)}/records${query({ type, name })}`,
    );
  },

  smtpProfiles(): Promise<{ items: SMTPProfile[] }> {
    return request("GET", "/api/v1/smtp-profiles");
  },

  createSMTPProfile(input: Record<string, unknown>): Promise<{ profile: SMTPProfile }> {
    return request("POST", "/api/v1/smtp-profiles", input);
  },

  updateSMTPProfile(id: string, input: Record<string, unknown>): Promise<{ profile: SMTPProfile }> {
    return request("PATCH", `/api/v1/smtp-profiles/${encodeURIComponent(id)}`, input);
  },

  deleteSMTPProfile(id: string): Promise<{ ok: boolean }> {
    return request("DELETE", `/api/v1/smtp-profiles/${encodeURIComponent(id)}`);
  },

  testSMTPProfile(id: string): Promise<{ ok: boolean; error?: string; result?: Record<string, unknown> }> {
    return request("POST", `/api/v1/smtp-profiles/${encodeURIComponent(id)}/test`, {});
  },

  storageProfiles(): Promise<{ items: StorageProfile[] }> {
    return request("GET", "/api/v1/storage-profiles");
  },

  createStorageProfile(input: Record<string, unknown>): Promise<{ profile: StorageProfile }> {
    return request("POST", "/api/v1/storage-profiles", input);
  },

  updateStorageProfile(id: string, input: Record<string, unknown>): Promise<{ profile: StorageProfile }> {
    return request("PATCH", `/api/v1/storage-profiles/${encodeURIComponent(id)}`, input);
  },

  deleteStorageProfile(id: string): Promise<{ ok: boolean }> {
    return request("DELETE", `/api/v1/storage-profiles/${encodeURIComponent(id)}`);
  },

  testStorageProfile(id: string): Promise<{ ok: boolean; error?: string; result?: Record<string, unknown> }> {
    return request("POST", `/api/v1/storage-profiles/${encodeURIComponent(id)}/test`, {});
  },

  backups(params: { instanceId?: string } = {}): Promise<{ items: Backup[]; total: number; totalBytes: number }> {
    return request("GET", `/api/v1/backups${query(params)}`);
  },

  createBackup(instanceId: string, note = ""): Promise<{ job: Job }> {
    return request("POST", "/api/v1/backups", { instanceId, note });
  },

  verifyBackup(id: string): Promise<{ job: Job }> {
    return request("POST", `/api/v1/backups/${encodeURIComponent(id)}/verify`, {});
  },

  restoreBackup(id: string, confirmation: string): Promise<{ job: Job; warning: string }> {
    return request("POST", `/api/v1/backups/${encodeURIComponent(id)}/restore`, { confirmation });
  },

  deleteBackup(id: string): Promise<{ ok: boolean }> {
    return request("DELETE", `/api/v1/backups/${encodeURIComponent(id)}?confirm=true`);
  },

  async downloadBackup(id: string): Promise<void> {
    const response = await fetch(`/api/v1/backups/${encodeURIComponent(id)}/download`, { credentials: "same-origin" });
    if (!response.ok) {
      if (response.status === 401) window.dispatchEvent(new CustomEvent("pbmanager:unauthorized"));
      throw new ApiError(response.status, "download_failed", "Could not download the backup");
    }
    const blob = await response.blob();
    const disposition = response.headers.get("content-disposition") ?? "";
    const match = /filename="?([^";]+)"?/i.exec(disposition);
    const name = match?.[1] ?? `backup-${id}`;
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  },

  jobs(params: { status?: string; type?: string; q?: string; page?: number; perPage?: number } = {}): Promise<
    Page<Job> & { counts: Record<string, number> }
  > {
    return request("GET", `/api/v1/jobs${query(params)}`);
  },

  job(id: string): Promise<{ job: Job }> {
    return request("GET", `/api/v1/jobs/${encodeURIComponent(id)}`);
  },

  cancelJob(id: string): Promise<{ ok: boolean }> {
    return request("POST", `/api/v1/jobs/${encodeURIComponent(id)}/cancel`, {});
  },

  retryJob(id: string): Promise<{ ok: boolean }> {
    return request("POST", `/api/v1/jobs/${encodeURIComponent(id)}/retry`, {});
  },

  audit(params: { action?: string; actor?: string; q?: string; page?: number; perPage?: number } = {}): Promise<Page<AuditEntry>> {
    return request("GET", `/api/v1/audit${query(params)}`);
  },

  settings(): Promise<SettingsView> {
    return request("GET", "/api/v1/settings");
  },

  updateSettings(config: ServerConfig): Promise<{ settings: SettingsView; changed: string[] }> {
    return request("PUT", "/api/v1/settings", config);
  },

  detectIP(): Promise<{ publicIpv4: string }> {
    return request("POST", "/api/v1/settings/detect-ip", {});
  },

  admins(): Promise<{ items: Admin[]; total: number }> {
    return request("GET", "/api/v1/admins");
  },

  createAdmin(input: { username: string; displayName: string; password: string; role: string }): Promise<{ admin: Admin }> {
    return request("POST", "/api/v1/admins", input);
  },

  updateAdmin(id: string, input: { displayName: string; role: string; disabled: boolean }): Promise<{ admin: Admin }> {
    return request("PATCH", `/api/v1/admins/${encodeURIComponent(id)}`, input);
  },

  deleteAdmin(id: string): Promise<{ ok: boolean }> {
    return request("DELETE", `/api/v1/admins/${encodeURIComponent(id)}`);
  },

  revokeAdminSessions(id: string): Promise<{ ok: boolean }> {
    return request("POST", `/api/v1/admins/${encodeURIComponent(id)}/revoke-sessions`, {});
  },
};

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  if (error instanceof Error) return error.message;
  return String(error);
}
