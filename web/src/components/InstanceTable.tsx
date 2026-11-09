import { Link } from "react-router-dom";
import { ArrowUpRight, Boxes, CircleSlash } from "lucide-react";
import type { Instance } from "../types";
import { formatBytes, formatRelative } from "../lib/format";
import { Badge, EmptyState, StatusBadge } from "./ui";

export function InstanceTable({ instances, compact = false }: { instances: Instance[]; compact?: boolean }) {
  if (instances.length === 0) {
    return (
      <EmptyState
        icon={<Boxes size={24} />}
        title="No instances yet"
        description="Provision a new PocketBase installation or import existing installations discovered on this server."
        action={
          <Link to="/instances" className="button button-primary button-md">
            Open instances
          </Link>
        }
      />
    );
  }
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            <th>Instance</th>
            <th>Status</th>
            <th>Port</th>
            <th>Version</th>
            {!compact ? <th>Memory</th> : null}
            {!compact ? <th>Disk</th> : null}
            <th>Last backup</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {instances.map((instance) => (
            <tr key={instance.id}>
              <td>
                <Link to={`/instances/${instance.id}`} className="table-primary">
                  <span className="instance-avatar">{instance.name.slice(0, 2).toUpperCase()}</span>
                  <span>
                    <strong>{instance.name}</strong>
                    <small>{instance.hostname}</small>
                  </span>
                </Link>
              </td>
              <td>
                <div className="status-stack">
                  <StatusBadge status={instance.status} />
                  <span className="muted small">{instance.health === "healthy" ? "Health OK" : instance.health}</span>
                </div>
              </td>
              <td className="mono">{instance.port}</td>
              <td>
                {instance.pbVersion ? <Badge tone="neutral">{instance.pbVersion}</Badge> : <span className="muted">—</span>}
              </td>
              {!compact ? <td>{instance.memoryBytes ? formatBytes(instance.memoryBytes) : <span className="muted">—</span>}</td> : null}
              {!compact ? <td>{instance.diskBytes ? formatBytes(instance.diskBytes) : <span className="muted">—</span>}</td> : null}
              <td>
                {instance.lastBackupAt ? (
                  <span className="muted">{formatRelative(instance.lastBackupAt)}</span>
                ) : (
                  <span className="inline-muted">
                    <CircleSlash size={13} /> none
                  </span>
                )}
              </td>
              <td className="table-action">
                <Link to={`/instances/${instance.id}`} className="icon-button" aria-label={`Open ${instance.name}`}>
                  <ArrowUpRight size={16} />
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
