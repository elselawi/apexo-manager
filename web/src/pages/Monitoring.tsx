import { Link } from "react-router-dom";
import {
  Activity,
  Archive,
  Boxes,
  CheckCircle2,
  Cpu,
  Globe2,
  HardDrive,
  MemoryStick,
  Network,
  Server,
  ShieldCheck,
} from "lucide-react";
import { api } from "../lib/api";
import { useDocumentTitle, usePolling } from "../lib/hooks";
import { formatBytes, formatDate, formatRelative, formatUptime, percent } from "../lib/format";
import {
  Badge,
  Card,
  EmptyState,
  ErrorBanner,
  LoadingBlock,
  PageHeader,
  StatCard,
  StatusBadge,
} from "../components/ui";
import { Gauge, Sparkline } from "../components/Charts";

export function MonitoringPage() {
  useDocumentTitle("Monitoring");
  const { data, error, loading, refresh } = usePolling((signal) => api.monitoring(), 15000);
  if (loading && !data) return <LoadingBlock label="Collecting monitoring data…" />;
  if (!data) return <ErrorBanner message={error ?? "Could not load monitoring"} onRetry={refresh} />;

  const points = data.serverMetrics;
  const cpuSeries = points.map((point) => point.cpuPercent);
  const memSeries = points.map((point) => (point.memTotal ? (point.memUsed / point.memTotal) * 100 : 0));
  const diskSeries = points.map((point) => (point.diskTotal ? (point.diskUsed / point.diskTotal) * 100 : 0));
  const latest = data.latest;
  const cpu = latest?.cpuPercent ?? 0;
  const memPercent = latest ? percent(latest.memUsed, latest.memTotal) : percent(data.server.memTotal - data.server.memAvailable, data.server.memTotal);
  const diskPercent = latest ? percent(latest.diskUsed, latest.diskTotal) : percent(data.server.diskTotal - data.server.diskFree, data.server.diskTotal);
  const activeInstances = data.instances.filter((instance) => instance.status === "running").length;
  const healthy = data.instances.filter((instance) => instance.health === "healthy").length;

  return (
    <div className="page">
      <PageHeader
        eyebrow="Observability"
        title="Monitoring"
        description="Low-frequency, low-overhead health history for the host, PocketBase processes, Caddy, and backups."
        actions={<ButtonRefresh onRefresh={refresh} />}
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      <div className="stats-grid">
        <StatCard label="Host uptime" value={formatUptime(data.server.uptimeSeconds)} detail={`Load ${data.server.load1.toFixed(2)} · ${data.server.cpus} vCPU`} icon={<Server size={20} />} tone="info" />
        <StatCard label="Running instances" value={`${activeInstances}/${data.instances.length}`} detail={`${healthy} healthy`} icon={<Boxes size={20} />} tone="success" />
        <StatCard label="Caddy" value={data.caddy.active ? "Active" : "Offline"} detail={data.caddy.configValid ? "configuration valid" : "configuration issue"} icon={<Network size={20} />} tone={data.caddy.active ? "success" : "danger"} />
        <StatCard label="Backups" value={data.backups.count} detail={`${formatBytes(data.backups.bytes)} · last ${formatRelative(data.backups.lastAt)}`} icon={<Archive size={20} />} tone="neutral" />
      </div>

      <div className="monitor-grid">
        <Card title="CPU utilization" subtitle={`${cpu.toFixed(1)}% current · 4-hour history`} className="chart-card">
          <Gauge value={cpu} label="CPU" detail={`${data.server.cpus} vCPU`} tone={cpu > 85 ? "danger" : cpu > 65 ? "warning" : "primary"} />
          <Sparkline values={cpuSeries} stroke="#0d9488" fill="rgba(13,148,136,.12)" />
          <div className="chart-caption"><span>oldest</span><span>{points.length} samples</span><span>now</span></div>
        </Card>
        <Card title="Memory" subtitle={`${formatBytes(latest?.memUsed ?? 0)} / ${formatBytes(latest?.memTotal ?? data.server.memTotal)}`} className="chart-card">
          <Gauge value={memPercent} label="Memory" detail={`${formatBytes(data.server.memAvailable)} available`} tone={memPercent > 90 ? "danger" : memPercent > 75 ? "warning" : "primary"} />
          <Sparkline values={memSeries} stroke="#2563eb" fill="rgba(37,99,235,.12)" />
          <div className="chart-caption"><span>oldest</span><span>{points.length} samples</span><span>now</span></div>
        </Card>
        <Card title="Disk" subtitle={`${formatBytes(data.server.diskTotal - data.server.diskFree)} / ${formatBytes(data.server.diskTotal)}`} className="chart-card">
          <Gauge value={diskPercent} label="Disk" detail={`${formatBytes(data.server.diskFree)} free`} tone={diskPercent > 90 ? "danger" : diskPercent > 75 ? "warning" : "primary"} />
          <Sparkline values={diskSeries} stroke="#7c3aed" fill="rgba(124,58,237,.12)" />
          <div className="chart-caption"><span>oldest</span><span>{points.length} samples</span><span>now</span></div>
        </Card>
      </div>

      <div className="two-column-grid">
        <Card title="Host details" subtitle={data.server.hostname}>
          <div className="host-details">
            <div><Server size={17} /><span>Operating system</span><strong>{data.server.os}</strong></div>
            <div><Cpu size={17} /><span>Kernel</span><strong className="mono">{data.server.kernel}</strong></div>
            <div><MemoryStick size={17} /><span>Memory</span><strong>{formatBytes(data.server.memTotal)}</strong></div>
            <div><HardDrive size={17} /><span>Data directory</span><strong className="mono">{data.server.dataDir}</strong></div>
          </div>
        </Card>
        <Card title="Platform status" subtitle="Manager-controlled components">
          <div className="platform-status">
            <div><CheckCircle2 size={17} /><span>Caddy service</span><StatusBadge status={data.caddy.active ? "active" : "stopped"} /></div>
            <div><ShieldCheck size={17} /><span>Caddy config</span><StatusBadge status={data.caddy.configValid ? "valid" : "failed"} /></div>
            <div><Globe2 size={17} /><span>Managed import</span><StatusBadge status={data.caddy.importInstalled ? "ok" : "missing"} /></div>
            <div><Activity size={17} /><span>Failed jobs</span><Badge tone={(data.jobCounts["failed"] ?? 0) > 0 ? "danger" : "success"}>{data.jobCounts["failed"] ?? 0}</Badge></div>
          </div>
          <div className="button-row">
            <Link to="/caddy" className="button button-secondary button-md">Caddy details</Link>
            <Link to="/jobs" className="button button-ghost button-md">Job queue</Link>
          </div>
        </Card>
      </div>

      <Card title="Instance health" subtitle="Latest samples and recent history" padded={false}>
        {data.instances.length === 0 ? (
          <EmptyState icon={<Boxes size={24} />} title="No instances to monitor" compact />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead><tr><th>Instance</th><th>Status</th><th>Health</th><th>Port</th><th>Memory</th><th>Disk</th><th>History</th><th /></tr></thead>
              <tbody>
                {data.instances.map((instance) => {
                  const sample = data.latestInstanceMetrics[instance.id];
                  const history = data.instanceMetrics[instance.id] ?? [];
                  return (
                    <tr key={instance.id}>
                      <td><Link to={`/instances/${instance.id}`} className="table-primary"><strong>{instance.name}</strong><small>{instance.hostname}</small></Link></td>
                      <td><StatusBadge status={sample?.status ?? instance.status} /></td>
                      <td><StatusBadge status={sample?.health ?? instance.health} /></td>
                      <td>{sample?.portListening ? <Badge tone="success">listening</Badge> : <Badge tone="neutral">closed</Badge>}</td>
                      <td>{formatBytes(sample?.memBytes)}</td>
                      <td>{formatBytes(sample?.diskBytes)}</td>
                      <td className="spark-cell">
                        {history.length > 1 ? <Sparkline values={history.map((point) => point.memBytes / (1024 * 1024))} height={28} stroke="#0d9488" fill="rgba(13,148,136,.08)" /> : <span className="muted">collecting</span>}
                      </td>
                      <td className="muted small">{sample ? formatDate(sample.capturedAt) : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <div className="monitor-footer">Samples are collected at a low, configurable interval and pruned automatically. Last refresh {formatDate(data.generatedAt)}.</div>
    </div>
  );
}

function ButtonRefresh({ onRefresh }: { onRefresh: () => void }) {
  return (
    <button className="button button-secondary button-md" onClick={onRefresh}>
      <Activity size={16} />
      <span>Refresh now</span>
    </button>
  );
}
