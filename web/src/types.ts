export type Role = "owner" | "admin" | "viewer";

export interface Admin {
  id: string;
  username: string;
  displayName: string;
  role: Role;
  disabled: boolean;
  createdAt: string;
  updatedAt: string;
  lastLoginAt?: string;
}

export interface Session {
  id: string;
  createdAt: string;
  expiresAt: string;
  lastSeenAt: string;
  ip: string;
  userAgent: string;
  revokedAt?: string;
  adminName?: string;
  adminUsername?: string;
  adminRole?: string;
}

export interface Instance {
  id: string;
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
  status: string;
  health: string;
  managed: boolean;
  source: string;
  notes: string;
  cloudflareAccountId?: string;
  smtpProfileId?: string;
  storageProfileId?: string;
  caddyMode: string;
  smtpApplyStatus: string;
  smtpAppliedAt?: string;
  storageApplyStatus: string;
  storageAppliedAt?: string;
  pbSuperuserEmail?: string;
  pbSuperuserManaged: boolean;
  pbLastVerifiedAt?: string;
  pbLastRepairedAt?: string;
  pbReconcileError?: string;
  pbAuthStatus: string;
  pbAuthCheckedAt?: string;
  pbAuthError?: string;
  backupEnabled: boolean;
  backupCron: string;
  backupRetention: number;
  nextBackupAt?: string;
  lastBackupAt?: string;
  lastHealthAt?: string;
  lastHealthError?: string;
  lastSeenAt?: string;
  createdAt: string;
  updatedAt: string;
  smtpProfileName?: string;
  storageProfileName?: string;
  cloudflareAccountName?: string;
  memoryBytes?: number;
  diskBytes?: number;
  cpuPercent?: number;
  portListening?: boolean;
  lastBackupStatus?: string;
  lastBackupSize?: number;
  openJobId?: string;
}

export interface Domain {
  id: string;
  instanceId: string;
  instanceName?: string;
  hostname: string;
  cloudflareAccountId?: string;
  zoneId?: string;
  zoneName?: string;
  dnsRecordId?: string;
  proxied: boolean;
  dnsManaged: boolean;
  caddyManaged: boolean;
  snippetPath?: string;
  dnsStatus: string;
  caddyStatus: string;
  tlsStatus: string;
  tlsExpiresAt?: string;
  lastCheckedAt?: string;
  lastError?: string;
  hostedActive: boolean;
  dismissable: boolean;
  hostedMessage: string;
  createdAt: string;
  updatedAt: string;
}

export interface AdminSettings {
  meta: { appName: string; appURL: string; senderName: string; senderAddress: string; hideControls: boolean };
  smtp: { enabled: boolean; host: string; port: number; username: string; password?: string; authMethod: string; tls: boolean; localName: string };
  s3: { enabled: boolean; bucket: string; region: string; endpoint: string; accessKey: string; secret?: string; forcePathStyle: boolean };
  backups: {
    cron: string;
    cronMaxKeep: number;
    s3: { enabled: boolean; bucket: string; region: string; endpoint: string; accessKey: string; secret?: string; forcePathStyle: boolean };
  };
  rateLimits: { enabled: boolean; rules: { label: string; audience: string; duration: number; maxRequests: number }[] };
  trustedProxy: { headers: string[]; useLeftmostIP: boolean };
  logs: { maxDays: number; minLevel: number; logIP: boolean; logAuthId: boolean };
  batch: { enabled: boolean; maxRequests: number; timeout: number; maxBodySize: number };
}

export interface NativeBackup {
  key: string;
  size: number;
  modified: string;
}

export interface AdminSuperuser {
  id: string;
  email: string;
  verified: boolean;
  created: string;
  updated: string;
}

export interface AuthCollection {
  id: string;
  name: string;
  type: string;
}

export type AuthRecord = Record<string, unknown> & { id: string };

export interface ApplicationConfig {
  smtp: {
    enabled: boolean;
    host: string;
    port: number;
    username: string;
    authMethod: string;
    tls: boolean;
    localName: string;
    fromAddress: string;
    fromName: string;
    passwordConfigured: boolean;
  };
  s3: {
    enabled: boolean;
    endpoint: string;
    bucket: string;
    region: string;
    accessKey: string;
    forcePathStyle: boolean;
    secretConfigured: boolean;
  };
  backups: { key: string; size: number; modified: string }[];
  fetchedAt: string;
}

export interface SMTPProfile {
  id: string;
  name: string;
  host: string;
  port: number;
  username: string;
  hasPassword: boolean;
  authMethod: string;
  tlsMode: "starttls" | "tls" | "none" | string;
  localName: string;
  fromAddress: string;
  fromName: string;
  status: string;
  lastTestedAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  usageCount?: number;
}

export interface StorageProfile {
  id: string;
  name: string;
  provider: "digitalocean" | "r2" | "aws" | "custom" | string;
  endpoint: string;
  region: string;
  bucket: string;
  accessKey: string;
  hasSecret: boolean;
  forcePathStyle: boolean;
  status: string;
  lastTestedAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
  usageCount?: number;
}

export interface CloudflareAccount {
  id: string;
  name: string;
  hasToken: boolean;
  accountId?: string;
  status: string;
  lastValidatedAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface Zone {
  id: string;
  name: string;
  status: string;
  accountName?: string;
}

export interface DNSRecord {
  id: string;
  type: string;
  name: string;
  content: string;
  proxied: boolean;
  ttl: number;
  comment?: string;
  modified_on?: string;
}

export interface Backup {
  id: string;
  instanceId: string;
  instanceName?: string;
  jobId?: string;
  method: "pocketbase_native" | "sqlite_snapshot" | string;
  filename: string;
  size: number;
  sha256: string;
  status: string;
  error?: string;
  note?: string;
  automatic: boolean;
  verifiedAt?: string;
  restoredAt?: string;
  createdAt: string;
}

export interface JobStep {
  id: string;
  jobId: string;
  name: string;
  status: string;
  detail: string;
  output?: string;
  attempt: number;
  startedAt?: string;
  finishedAt?: string;
}

export interface Job {
  id: string;
  type: string;
  payload: string;
  status: string;
  priority: number;
  attempts: number;
  maxAttempts: number;
  availableAt: string;
  startedAt?: string;
  finishedAt?: string;
  lastError?: string;
  progress: number;
  result?: string;
  createdBy: string;
  canceled: boolean;
  recoveredCount: number;
  createdAt: string;
  updatedAt: string;
  steps?: JobStep[];
}

export interface AuditEntry {
  id: string;
  actorType: string;
  actorId?: string;
  actorName: string;
  action: string;
  targetType?: string;
  targetId?: string;
  detail?: string;
  ip?: string;
  requestId?: string;
  createdAt: string;
}

export interface ServerMetrics {
  id?: number;
  capturedAt: string;
  cpuPercent: number;
  memUsed: number;
  memTotal: number;
  diskUsed: number;
  diskTotal: number;
  load1: number;
  uptimeSeconds: number;
}

export interface InstanceMetrics {
  id?: number;
  instanceId: string;
  capturedAt: string;
  status: string;
  health: string;
  memBytes: number;
  diskBytes: number;
  cpuPercent: number;
  portListening: boolean;
  error?: string;
}

export interface ServerInfo {
  hostname: string;
  os: string;
  kernel: string;
  arch: string;
  cpus: number;
  uptimeSeconds: number;
  load1: number;
  memTotal: number;
  memAvailable: number;
  diskTotal: number;
  diskFree: number;
  dataDir: string;
}

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  perPage: number;
}

export interface Overview {
  instances: Instance[];
  instanceCounts: Record<string, number>;
  jobCounts: Record<string, number>;
  backups: { count: number; bytes: number; lastAt: string };
  server: ServerInfo;
  serverMetrics: ServerMetrics | null;
  activeJobs: Job[];
  failedJobs: Job[];
  recentJobs: Job[];
  recentAudit: AuditEntry[];
  alerts: { level: string; title: string; message: string; target?: string }[];
  generatedAt: string;
}

export interface MonitoringData {
  server: ServerInfo;
  serverMetrics: ServerMetrics[];
  latest: ServerMetrics | null;
  instances: Instance[];
  instanceMetrics: Record<string, InstanceMetrics[]>;
  latestInstanceMetrics: Record<string, InstanceMetrics>;
  caddy: CaddyStatus;
  jobCounts: Record<string, number>;
  backups: { count: number; bytes: number; lastAt: string };
  generatedAt: string;
}

export interface ServerConfig {
  installRoot: string;
  archiveTemplate: string;
  downloadUrlTemplate: string;
  checksumUrlTemplate: string;
  defaultPbVersion: string;
  pbDataDirTemplate: string;
  serviceDir: string;
  caddyfilePath: string;
  caddySnippetDir: string;
  caddyService: string;
  caddyBinary: string;
  systemctlBinary: string;
  journalctlBinary: string;
  backupDir: string;
  discoveryRoots: string[];
  publicIpv4: string;
  portStart: number;
  portEnd: number;
  serviceUserPrefix: string;
  monitorIntervalSeconds: number;
  jobRetentionDays: number;
  auditRetentionDays: number;
  backupRetentionDefault: number;
  verifyHttps: boolean;
  defaultCaddyEnabled: boolean;
  defaultDnsEnabled: boolean;
  requireChecksums: boolean;
}

export interface ManagerUpdate {
  current: string;
  latest?: string;
  tag?: string;
  name?: string;
  notes?: string;
  releaseUrl?: string;
  publishedAt?: string;
  available: boolean;
  reason?: string;
  assetName?: string;
  assetSize?: number;
  downloadUrl?: string;
  sha256?: string;
  checkedAt: string;
}

export interface SettingsView {
  config: ServerConfig;
  bootstrap: {
    configPath: string;
    dataDir: string;
    dbPath: string;
    listenAddr: string;
    publicUrl: string;
    note: string;
  };
  warnings?: string[];
  updatedAt?: string;
}

export interface InstanceDefaults {
  baseDir: string;
  dataDir: string;
  binaryPath: string;
  serviceName: string;
  serviceFile: string;
  runAsUser: string;
  port: number;
  pbVersion: string;
  arch: string;
  backupCron: string;
  backupRetention: number;
  warnings?: string[];
}

export interface Discovered {
  key: string;
  name: string;
  hostname: string;
  baseDir: string;
  binaryPath: string;
  dataDir: string;
  port: number;
  serviceName: string;
  serviceFile: string;
  runAsUser?: string;
  version?: string;
  running: boolean;
  domains?: string[];
  evidence: string[];
  warnings?: string[];
  tracked: boolean;
  managed: boolean;
}

export interface CaddyStatus {
  available: boolean;
  active: boolean;
  version?: string;
  configValid: boolean;
  importInstalled: boolean;
  caddyfile: string;
  snippetDir: string;
  error?: string;
  service: string;
}

export interface CaddySite {
  addresses: string[];
  upstreams: string[];
  managed: boolean;
  line: number;
}

export interface CaddySnippet {
  name: string;
  path: string;
  domains: string[];
  upstreams: string[];
  managed: boolean;
  modifiedAt: string;
}

export interface CaddyConfigView {
  status: CaddyStatus;
  sites: CaddySite[];
  imports: string[];
  snippets: CaddySnippet[];
  error?: string;
}

export interface InstanceDetail {
  instance: Instance;
  domains: Domain[];
  backups: Backup[];
  jobs: Job[];
  metrics: InstanceMetrics[];
  currentMetrics?: InstanceMetrics;
  url: string;
  capabilities: {
    systemd: boolean;
    caddy: boolean;
    dns: boolean;
    pocketbaseAuth: boolean;
  };
}

export interface InstanceCreateInput {
  name: string;
  hostname: string;
  pbVersion: string;
  arch: string;
  port: number;
  baseDir: string;
  dataDir: string;
  binaryPath?: string;
  serviceName: string;
  serviceFile: string;
  runAsUser: string;
  createSystemUser: boolean;
  allowRootUser: boolean;
  domain: string;
  cloudflareAccountId: string;
  manageCaddy: boolean;
  manageDns: boolean;
  proxied: boolean;
  smtpProfileId: string;
  storageProfileId: string;
  backupEnabled: boolean;
  backupCron: string;
  backupRetention: number;
  notes: string;
  checksum: string;
}

export interface Diagnostic {
  dataDir: string;
  dbPath: string;
  dbExists: boolean;
  dbPermissions: string;
  keyExists: boolean;
  keyPermissions: string;
  sqliteCheck: string;
  migrations: number[];
  adminCount: number;
  instanceCount: number;
  serviceActive: boolean;
  warnings: string[] | null;
  errors: string[] | null;
}

export interface MeResponse {
  admin: Admin;
  csrfToken: string;
  expiresAt: string;
  sessionId: string;
  serverTime: string;
}

export interface AuthStatus {
  initialized: boolean;
  version: string;
}

export interface VerifyInstanceResult {
  status: string;
  health: string;
  healthError?: string;
  portListening: boolean;
  systemd: Record<string, unknown>;
  systemdError?: string;
  metrics: InstanceMetrics;
  checkedAt: string;
}
