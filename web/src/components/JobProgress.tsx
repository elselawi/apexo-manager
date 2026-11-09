import { useEffect, useRef, useState } from "react";
import { CheckCircle2, Circle, Loader2, XCircle } from "lucide-react";
import { api } from "../lib/api";
import type { Job, JobStep } from "../types";
import { formatDate, jobTypeLabel, statusTone } from "../lib/format";
import { Badge, Button, Card, CodeBlock, ProgressBar, StatusBadge } from "./ui";

export function JobProgress({
  jobId,
  onFinished,
  dismissible = false,
  onDismiss,
}: {
  jobId: string;
  onFinished?: (job: Job) => void;
  dismissible?: boolean;
  onDismiss?: () => void;
}) {
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState<string | null>(null);
  const onFinishedRef = useRef(onFinished);

  useEffect(() => {
    onFinishedRef.current = onFinished;
  }, [onFinished]);

  useEffect(() => {
    let active = true;
    let timer: number | undefined;
    let finished = false;
    async function load() {
      try {
        const result = await api.job(jobId);
        if (!active) return;
        setJob(result.job);
        setError(null);
        if (["succeeded", "failed", "canceled"].includes(result.job.status)) {
          if (!finished) {
            finished = true;
            onFinishedRef.current?.(result.job);
          }
          return;
        }
      } catch (err) {
        if (active) setError(err instanceof Error ? err.message : String(err));
      }
      if (active && !finished) timer = window.setTimeout(load, 1800);
    }
    void load();
    return () => {
      active = false;
      if (timer) window.clearTimeout(timer);
    };
  }, [jobId]);

  if (!job) {
    return (
      <Card className="job-progress-card">
        <div className="muted">{error ?? "Loading job…"}</div>
      </Card>
    );
  }

  const active = job.status === "running" || job.status === "queued";
  return (
    <Card
      className="job-progress-card"
      title={
        <span className="job-title">
          <span>{jobTypeLabel(job.type)}</span>
          <StatusBadge status={job.status} />
        </span>
      }
      subtitle={`Job ${job.id} · started ${formatDate(job.startedAt || job.createdAt)}`}
      actions={
        dismissible && !active ? (
          <Button size="sm" variant="ghost" onClick={onDismiss}>
            Dismiss
          </Button>
        ) : null
      }
    >
      <div className="job-progress-head">
        <ProgressBar value={job.progress} tone={job.status === "failed" ? "danger" : job.status === "succeeded" ? "success" : "primary"} />
        <span className="muted">{job.progress}%</span>
      </div>
      {job.lastError ? <div className="form-error">{job.lastError}</div> : null}
      <div className="job-steps">
        {(job.steps ?? []).map((step) => (
          <StepRow key={step.id} step={step} />
        ))}
      </div>
      {job.result && job.status === "succeeded" ? <CodeBlock maxHeight={180}>{pretty(job.result)}</CodeBlock> : null}
      {error ? <div className="form-error">{error}</div> : null}
    </Card>
  );
}

function StepRow({ step }: { step: JobStep }) {
  const icon =
    step.status === "succeeded" || step.status === "skipped" ? (
      <CheckCircle2 size={16} className="text-success" />
    ) : step.status === "failed" ? (
      <XCircle size={16} className="text-danger" />
    ) : step.status === "running" ? (
      <Loader2 size={16} className="spin text-primary" />
    ) : (
      <Circle size={16} className="text-muted" />
    );
  return (
    <div className={`job-step job-step-${step.status}`}>
      <span className="job-step-icon">{icon}</span>
      <div>
        <div className="job-step-name">{step.name}</div>
        {step.detail ? <div className="muted small">{step.detail}</div> : null}
        {step.output && step.status === "succeeded" ? <div className="job-step-output">{step.output}</div> : null}
      </div>
      <Badge tone={statusTone(step.status)}>{step.status}</Badge>
    </div>
  );
}

function pretty(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
