import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { FileClock, RefreshCw, RotateCcw, XCircle } from "lucide-react";
import { api, errorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useDocumentTitle, usePolling, useDebouncedValue } from "../lib/hooks";
import { useToast } from "../lib/toast";
import { formatDate, formatRelative, jobTypeLabel, titleCase } from "../lib/format";
import type { Job } from "../types";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorBanner,
  IconButton,
  LoadingBlock,
  Modal,
  PageHeader,
  Pagination,
  ProgressBar,
  SearchInput,
  Select,
  StatusBadge,
} from "../components/ui";
import { JobProgress } from "../components/JobProgress";

const jobTypes = [
  "instance.provision",
  "instance.update",
  "instance.start",
  "instance.stop",
  "instance.restart",
  "instance.delete",
  "instance.apply-smtp",
  "instance.apply-storage",
  "instance.sync-caddy",
  "domain.attach",
  "domain.remove",
  "backup.create",
  "backup.verify",
  "backup.restore",
  "caddy.ensure-import",
];

export function JobsPage() {
  useDocumentTitle("Background Jobs");
  const { admin } = useAuth();
  const canWrite = admin?.role !== "viewer";
  const toast = useToast();
  const [searchParams, setSearchParams] = useSearchParams();
  const [status, setStatus] = useState(searchParams.get("status") ?? "");
  const [type, setType] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [selectedId, setSelectedId] = useState<string | null>(searchParams.get("job"));
  const debounced = useDebouncedValue(search, 250);
  const { data, error, loading, refresh } = usePolling(
    (signal) => api.jobs({ status, type, q: debounced, page, perPage: 30 }),
    6000,
    [status, type, debounced, page],
  );

  useEffect(() => {
    const job = searchParams.get("job");
    if (job) setSelectedId(job);
  }, [searchParams]);

  function selectJob(job: Job) {
    setSelectedId(job.id);
    const next = new URLSearchParams(searchParams);
    next.set("job", job.id);
    setSearchParams(next, { replace: true });
  }

  function closeJob() {
    setSelectedId(null);
    const next = new URLSearchParams(searchParams);
    next.delete("job");
    setSearchParams(next, { replace: true });
  }

  const jobs = data?.items ?? [];
  const counts = data?.counts ?? {};

  return (
    <div className="page">
      <PageHeader
        eyebrow="Operations"
        title="Background jobs"
        description="Persistent execution history with checkpoints, bounded retries, cancellation, and crash recovery."
        actions={<Button variant="secondary" icon={<RefreshCw size={15} />} onClick={refresh}>Refresh</Button>}
      />
      {error ? <ErrorBanner message={error} onRetry={refresh} /> : null}

      <div className="job-status-strip">
        {["running", "queued", "succeeded", "failed", "canceled"].map((state) => (
          <button key={state} className={`job-status-pill ${status === state ? "active" : ""}`} onClick={() => { setStatus(status === state ? "" : state); setPage(1); }}>
            <span className={`status-dot status-${state}`} />
            <span>{titleCase(state)}</span>
            <strong>{counts[state] ?? 0}</strong>
          </button>
        ))}
      </div>

      <Card
        title="Job queue"
        subtitle="Closing the browser does not interrupt a job."
        actions={
          <div className="button-row">
            <Select value={type} onChange={(event) => { setType(event.target.value); setPage(1); }} className="compact-select">
              <option value="">All job types</option>
              {jobTypes.map((jobType) => <option value={jobType} key={jobType}>{jobTypeLabel(jobType)}</option>)}
            </Select>
          </div>
        }
        padded={false}
      >
        <div className="toolbar compact">
          <SearchInput value={search} onChange={(value) => { setSearch(value); setPage(1); }} placeholder="Search job ID, type, payload, error…" />
          <Button size="sm" variant="ghost" onClick={() => { setStatus(""); setType(""); setSearch(""); setPage(1); }}>Clear filters</Button>
        </div>
        {loading && !data ? <LoadingBlock /> : jobs.length === 0 ? (
          <EmptyState icon={<FileClock size={26} />} title="No jobs match" description="Provisioning, backups, DNS, and Caddy changes all appear here." />
        ) : (
          <div className="table-wrap">
            <table className="data-table clickable">
              <thead><tr><th>Job</th><th>Type</th><th>Status</th><th>Progress</th><th>Attempts</th><th>Created</th><th /></tr></thead>
              <tbody>
                {jobs.map((job) => (
                  <tr key={job.id} onClick={() => selectJob(job)} tabIndex={0} onKeyDown={(event) => event.key === "Enter" && selectJob(job)}>
                    <td><span className="mono small">{job.id.slice(0, 12)}</span>{job.recoveredCount > 0 ? <Badge tone="warning">recovered {job.recoveredCount}</Badge> : null}</td>
                    <td><strong>{jobTypeLabel(job.type)}</strong>{job.lastError ? <small className="text-danger">{job.lastError.slice(0, 90)}</small> : null}</td>
                    <td><StatusBadge status={job.status} /></td>
                    <td className="progress-cell"><ProgressBar value={job.progress} tone={job.status === "failed" ? "danger" : job.status === "succeeded" ? "success" : "primary"} /><span>{job.progress}%</span></td>
                    <td>{job.attempts}/{job.maxAttempts}</td>
                    <td className="muted">{formatRelative(job.createdAt)}</td>
                    <td className="table-action"><Button size="sm" variant="ghost">Inspect</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="pagination-wrap">
          <span className="muted small">{data?.total ?? 0} jobs</span>
          <Pagination page={page} perPage={30} total={data?.total ?? 0} onPage={setPage} />
        </div>
      </Card>

      <JobDetailModal
        jobId={selectedId}
        canWrite={canWrite}
        onClose={closeJob}
        onChanged={refresh}
      />
    </div>
  );
}

function JobDetailModal({ jobId, canWrite, onClose, onChanged }: { jobId: string | null; canWrite: boolean; onClose: () => void; onChanged: () => void }) {
  const toast = useToast();
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [actionBusy, setActionBusy] = useState(false);

  useEffect(() => {
    if (!jobId) {
      setJob(null);
      return;
    }
    setError(null);
    api.job(jobId).then((result) => setJob(result.job)).catch((err) => setError(errorMessage(err)));
  }, [jobId]);

  async function cancel() {
    if (!job) return;
    setActionBusy(true);
    try {
      await api.cancelJob(job.id);
      toast.warning("Cancellation requested");
      onChanged();
      const result = await api.job(job.id);
      setJob(result.job);
    } catch (err) {
      toast.error("Could not cancel", errorMessage(err));
    } finally {
      setActionBusy(false);
    }
  }

  async function retry() {
    if (!job) return;
    setActionBusy(true);
    try {
      await api.retryJob(job.id);
      toast.success("Job queued for retry");
      onChanged();
      const result = await api.job(job.id);
      setJob(result.job);
    } catch (err) {
      toast.error("Could not retry", errorMessage(err));
    } finally {
      setActionBusy(false);
    }
  }

  return (
    <Modal open={Boolean(jobId)} onClose={onClose} title="Job detail" description={job ? `${job.id} · ${jobTypeLabel(job.type)}` : undefined} size="lg" footer={<><Button onClick={onClose}>Close</Button>{job && canWrite && ["queued", "running"].includes(job.status) ? <Button variant="danger" icon={<XCircle size={15} />} onClick={() => void cancel()} loading={actionBusy}>Cancel job</Button> : null}{job && canWrite && ["failed", "canceled"].includes(job.status) ? <Button variant="primary" icon={<RotateCcw size={15} />} onClick={() => void retry()} loading={actionBusy}>Retry job</Button> : null}</>}>
      {error ? <ErrorBanner message={error} /> : null}
      {!job ? (
        <LoadingBlock label="Loading job…" />
      ) : (
        <JobProgress jobId={job.id} onFinished={() => { onChanged(); void api.job(job.id).then((result) => setJob(result.job)); }} />
      )}
      {job ? (
        <div className="job-meta-grid">
          <div><span>Created</span><strong>{formatDate(job.createdAt)}</strong></div>
          <div><span>Started</span><strong>{formatDate(job.startedAt)}</strong></div>
          <div><span>Finished</span><strong>{formatDate(job.finishedAt)}</strong></div>
          <div><span>Created by</span><strong className="mono">{job.createdBy || "system"}</strong></div>
          <div><span>Available</span><strong>{formatRelative(job.availableAt)}</strong></div>
          <div><span>Recovered</span><strong>{job.recoveredCount} times</strong></div>
        </div>
      ) : null}
    </Modal>
  );
}
