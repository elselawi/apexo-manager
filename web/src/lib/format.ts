export function formatBytes(bytes: number | undefined | null, digits = 1): string {
  if (bytes === undefined || bytes === null || Number.isNaN(bytes)) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const index = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** index;
  return `${value.toFixed(index === 0 ? 0 : digits)} ${units[index]}`;
}

export function formatDate(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatRelative(value?: string | null): string {
  if (!value) return "never";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const diff = Date.now() - date.getTime();
  const abs = Math.abs(diff);
  const suffix = diff >= 0 ? "ago" : "from now";
  if (abs < 60_000) return "just now";
  if (abs < 3_600_000) return `${Math.round(abs / 60_000)}m ${suffix}`;
  if (abs < 86_400_000) return `${Math.round(abs / 3_600_000)}h ${suffix}`;
  return `${Math.round(abs / 86_400_000)}d ${suffix}`;
}

export function formatUptime(seconds: number | undefined): string {
  if (!seconds || seconds < 0) return "—";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

export function percent(used: number, total: number): number {
  if (!total || total <= 0) return 0;
  return Math.max(0, Math.min(100, (used / total) * 100));
}

export type Tone = "success" | "warning" | "danger" | "neutral" | "info";

export function statusTone(status: string | undefined | null): Tone {
  switch ((status ?? "").toLowerCase()) {
    case "running":
    case "healthy":
    case "succeeded":
    case "completed":
    case "verified":
    case "ok":
    case "valid":
    case "active":
    case "linked":
    case "applied":
    case "validated":
      return "success";
    case "provisioning":
    case "queued":
    case "pending":
    case "expiring":
    case "warning":
    case "unvalidated":
    case "created":
    case "auth_missing":
    case "not_applied":
      return "warning";
    case "failed":
    case "unhealthy":
    case "error":
    case "canceled":
    case "missing":
    case "not-found":
      return "danger";
    case "stopped":
    case "down":
    case "unmanaged":
    case "unlinked":
    case "unknown":
      return "neutral";
    default:
      return "info";
  }
}

export function titleCase(value: string | undefined | null): string {
  if (!value) return "—";
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

export function truncateMiddle(value: string, max = 42): string {
  if (value.length <= max) return value;
  const edge = Math.floor((max - 1) / 2);
  return `${value.slice(0, edge)}…${value.slice(-edge)}`;
}

export function jobTypeLabel(type: string): string {
  const map: Record<string, string> = {
    "instance.provision": "Provision instance",
    "instance.update": "Update PocketBase",
    "instance.start": "Start instance",
    "instance.stop": "Stop instance",
    "instance.restart": "Restart instance",
    "instance.delete": "Delete instance",
    "instance.apply-smtp": "Apply SMTP settings",
    "instance.apply-storage": "Apply S3 settings",
    "instance.sync-caddy": "Sync Caddy routes",
    "domain.attach": "Attach domain",
    "domain.remove": "Remove domain",
    "backup.create": "Create backup",
    "backup.verify": "Verify backup",
    "backup.restore": "Restore backup",
    "caddy.ensure-import": "Install Caddy import",
    "manager.update": "Update pbmanager",
  };
  return map[type] ?? titleCase(type);
}
