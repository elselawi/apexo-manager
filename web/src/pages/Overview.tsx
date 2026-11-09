import { Link } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  Archive,
  ArrowRight,
  Boxes,
  CheckCircle2,
  Cpu,
  HardDrive,
  Plus,
  ServerCog,
  ShieldAlert,
} from "lucide-react";
import { api } from "../lib/api";
import { formatBytes, formatDate, formatRelative, formatUptime, percent, titleCase } from "../lib/format";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { Badge, Button, Card, EmptyState, ErrorBanner, LoadingBlock, PageHeader, StatCard, StatusBadge } from "../components/ui";
import { InstanceTable } from "../components/InstanceTable";
import { JobProgress } from "../components/JobProgress";
import { Gauge } from "../components/Charts";

export function OverviewPage() {
  useDocumentTitle("Overview");
  const { data, error, loading, refresh } = usePolling((signal) => api.overview(), 15000);
  if (loading && !data) return <LoadingBlock label="Loading infrastructure overview…" />;
  if (!data) return <ErrorBanner message={error ?? "Could not load the overview"} onRetry={refresh} />;

  const counts = data.instanceCounts;
  const instances = data.instances ?? [];
  const alerts = data.alerts ?? [];
  const recentJobs = data.recentJobs ?? [];
  const recentAudit = data.recentAudit ?? [];
  const activeJobs = data.activeJobs ?? [];
  const running = counts["running"] ?? 0;
  const total = instances.length;
  const failedJobs = data.jobCounts["failed"] ?? 0;
  const server = data.server;
  const metrics = data.serverMetrics;
  const cpu = metrics?.cpuPercent ?? 0;
  const memUsed = metrics?.memUsed ?? server.memTotal - server.memAvailable;
  const memPercent = percent(memUsed, server.memTotal);
  const diskPercent = percent(server.diskTotal - server.diskFree, server.diskTotal);
  const activeJob = activeJobs[0];

  return (
    <div className="page">
      <PageHeader
        eyebrow="Operations"
        title="Infrastructure overview"
        description="Live status for PocketBase instances, the host, and the manager's background operations."
        actions={
          <>
            <Button variant="secondary" icon={<Boxes size={16} />} onClick={() => (window.location.href = "/instances")}>
              All instances
            </Button>
            <Link to="/instances/new" className="button button-primary button-md">
              <Plus size={16} />
              <span>New instance</span>
            </Link>
          </>
        }
      />

      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      {alerts.length > 0 ? (
        <div className="alert-grid">
          {alerts.map((alert, index) => (
            <Link to={alert.target ?? "#"} key={`${alert.title}-${index}`} className={`alert-card alert-${alert.level}`}>
              {alert.level === "critical" ? <ShieldAlert size={19} /> : <AlertTriangle size={19} />}
              <div>
                <strong>{alert.title}</strong>
                <span>{alert.message}</span>
              </div>
              <ArrowRight size={16} />
            </Link>
          ))}
        </div>
      ) : null}

      <div className="stats-grid">
        <StatCard
          label="Instances"
          value={total}
          detail={`${running} running · ${counts["stopped"] ?? 0} stopped`}
          icon={<Boxes size={20} />}
          tone="info"
        />
        <StatCard
          label="Healthy"
          value={instances.filter((instance) => instance.health === "healthy").length}
          detail={`${instances.filter((instance) => instance.health !== "healthy" && instance.status === "running").length} need attention`}
          icon={<CheckCircle2 size={20} />}
          tone="success"
        />
        <StatCard
          label="Host load"
          value={server.load1.toFixed(2)}
          detail={`${server.cpus} vCPU · up ${formatUptime(server.uptimeSeconds)}`}
          icon={<Cpu size={20} />}
          tone="warning"
        />
        <StatCard
          label="Failed jobs"
          value={failedJobs}
          detail={`${data.jobCounts["running"] ?? 0} running · ${data.jobCounts["queued"] ?? 0} queued`}
          icon={<Activity size={20} />}
          tone={failedJobs > 0 ? "danger" : "neutral"}
        />
      </div>

      <div className="overview-grid">
        <Card
          title="Server resources"
          subtitle={`${server.os} · ${server.hostname}`}
          actions={
            <Link to="/monitoring" className="text-link">
              Monitoring <ArrowRight size={14} />
            </Link>
          }
        >
          <div className="gauges-grid">
            <Gauge value={cpu} label="CPU" detail={`${server.cpus} vCPU`} tone={cpu > 85 ? "danger" : cpu > 65 ? "warning" : "primary"} />
            <Gauge
              value={memPercent}
              label="Memory"
              detail={`${formatBytes(memUsed)} / ${formatBytes(server.memTotal)}`}
              tone={memPercent > 90 ? "danger" : memPercent > 75 ? "warning" : "primary"}
            />
            <Gauge
              value={diskPercent}
              label="Disk"
              detail={`${formatBytes(server.diskTotal - server.diskFree)} / ${formatBytes(server.diskTotal)}`}
              tone={diskPercent > 90 ? "danger" : diskPercent > 75 ? "warning" : "primary"}
            />
          </div>
          <div className="server-foot">
            <div>
              <ServerCog size={16} />
              <span>{server.kernel}</span>
            </div>
            <div>
              <HardDrive size={16} />
              <span>{formatBytes(server.diskFree)} free</span>
            </div>
            <div>
              <Archive size={16} />
              <span>{data.backups.count} backups · {formatBytes(data.backups.bytes)}</span>
            </div>
          </div>
        </Card>

        <Card title="Background activity" subtitle={activeJob ? "The job queue is persistent across restarts." : "No jobs are running."}>
          {activeJob ? (
            <JobProgress jobId={activeJob.id} />
          ) : recentJobs.length > 0 ? (
            <div className="timeline">
              {recentJobs.slice(0, 5).map((job) => (
                <Link to={`/jobs?job=${job.id}`} key={job.id} className="timeline-row">
                  <span className={`timeline-dot timeline-${job.status}`} />
                  <div>
                    <strong>{titleCase(job.type.replace(".", " "))}</strong>
                    <span>{formatRelative(job.createdAt)}</span>
                  </div>
                  <StatusBadge status={job.status} />
                </Link>
              ))}
            </div>
          ) : (
            <EmptyState icon={<Activity size={22} />} title="Quiet queue" description="Provisioning, backups, and infrastructure changes appear here." compact />
          )}
        </Card>
      </div>

      <Card
        title="PocketBase instances"
        subtitle={`${total} tracked ${total === 1 ? "installation" : "installations"}`}
        actions={
          <Link to="/instances" className="text-link">
            View all <ArrowRight size={14} />
          </Link>
        }
        padded={false}
      >
        <InstanceTable instances={instances.slice(0, 8)} compact />
      </Card>

      <div className="overview-grid">
        <Card title="Recent activity" subtitle="Administrative and system events">
          {recentAudit.length === 0 ? (
            <EmptyState icon={<Activity size={22} />} title="No activity yet" compact />
          ) : (
            <div className="audit-list">
              {recentAudit.map((entry) => (
                <div className="audit-list-row" key={entry.id}>
                  <span className="audit-icon">
                    <Activity size={15} />
                  </span>
                  <div>
                    <strong>{titleCase(entry.action.replaceAll(".", " "))}</strong>
                    <span>
                      {entry.actorName || entry.actorType} · {formatDate(entry.createdAt)}
                    </span>
                  </div>
                  <Badge tone="neutral">{entry.actorType}</Badge>
                </div>
              ))}
            </div>
          )}
        </Card>
        <Card title="Backup posture" subtitle="Verified artifacts on local storage">
          <div className="backup-posture">
            <div className="backup-posture-value">{data.backups.count}</div>
            <div>
              <strong>Verified backups</strong>
              <span>{formatBytes(data.backups.bytes)} stored locally</span>
              <span>Last: {data.backups.lastAt ? formatDate(data.backups.lastAt) : "never"}</span>
            </div>
          </div>
          <div className="button-row">
            <Link to="/backups" className="button button-secondary button-md">
              Backup history
            </Link>
            <Link to="/instances" className="button button-ghost button-md">
              Configure schedules
            </Link>
          </div>
        </Card>
      </div>
    </div>
  );
}
