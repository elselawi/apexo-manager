import {
  useEffect,
  useId,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { createPortal } from "react-dom";
import {
  AlertTriangle,
  Check,
  ChevronLeft,
  ChevronRight,
  Copy,
  Loader2,
  Search,
  X,
} from "lucide-react";
import { statusTone, titleCase, type Tone } from "../lib/format";

export function Button({
  variant = "secondary",
  size = "md",
  loading = false,
  icon,
  children,
  className = "",
  disabled,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "danger" | "ghost" | "link";
  size?: "sm" | "md";
  loading?: boolean;
  icon?: ReactNode;
}) {
  return (
    <button
      className={`button button-${variant} button-${size} ${className}`}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? <Loader2 size={15} className="spin" /> : icon}
      {children ? <span>{children}</span> : null}
    </button>
  );
}

export function IconButton({
  label,
  children,
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; children: ReactNode }) {
  return (
    <button className={`icon-button ${className}`} aria-label={label} title={label} {...props}>
      {children}
    </button>
  );
}

export function Badge({ tone = "neutral", children, dot = false }: { tone?: Tone; children: ReactNode; dot?: boolean }) {
  return (
    <span className={`badge badge-${tone}`}>
      {dot ? <span className="badge-dot" /> : null}
      {children}
    </span>
  );
}

export function StatusBadge({ status, label }: { status?: string | null; label?: string }) {
  return (
    <Badge tone={statusTone(status)} dot>
      {label ?? titleCase(status)}
    </Badge>
  );
}

export function Card({
  id,
  title,
  subtitle,
  actions,
  children,
  className = "",
  padded = true,
}: {
  id?: string;
  title?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  padded?: boolean;
}) {
  return (
    <section className={`card ${className}`} id={id}>
      {title || actions || subtitle ? (
        <header className="card-header">
          <div>
            {title ? <h2 className="card-title">{title}</h2> : null}
            {subtitle ? <p className="card-subtitle">{subtitle}</p> : null}
          </div>
          {actions ? <div className="card-actions">{actions}</div> : null}
        </header>
      ) : null}
      <div className={padded ? "card-body" : "card-body card-body-flush"}>{children}</div>
    </section>
  );
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="page-header">
      <div>
        {eyebrow ? <div className="eyebrow">{eyebrow}</div> : null}
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </div>
  );
}

export function Field({
  label,
  hint,
  error,
  children,
  htmlFor,
  required,
}: {
  label: string;
  hint?: ReactNode;
  error?: string | null;
  children: ReactNode;
  htmlFor?: string;
  required?: boolean;
}) {
  return (
    <div className="field">
      <label htmlFor={htmlFor} className="field-label">
        {label}
        {required ? <span className="required"> *</span> : null}
      </label>
      {children}
      {error ? <div className="field-error">{error}</div> : hint ? <div className="field-hint">{hint}</div> : null}
    </div>
  );
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`input ${props.className ?? ""}`} />;
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={`input textarea ${props.className ?? ""}`} />;
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`input select ${props.className ?? ""}`} />;
}

export function Switch({
  checked,
  onChange,
  label,
  description,
  disabled,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  description?: string;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <label className={`switch-field ${disabled ? "disabled" : ""}`} htmlFor={id}>
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        className={`switch ${checked ? "switch-on" : ""}`}
        onClick={() => onChange(!checked)}
        disabled={disabled}
      >
        <span className="switch-knob" />
      </button>
      <span>
        <span className="switch-label">{label}</span>
        {description ? <span className="switch-description">{description}</span> : null}
      </span>
    </label>
  );
}

export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  size = "md",
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  size?: "sm" | "md" | "lg" | "xl";
}) {
  const panelRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    window.setTimeout(() => {
      const focusable = panelRef.current?.querySelector<HTMLElement>(
        "input, select, textarea, button, [href], [tabindex]:not([tabindex='-1'])",
      );
      focusable?.focus();
    }, 30);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = previous;
    };
  }, [open, onClose]);
  if (!open) return null;
  return createPortal(
    <div className="modal-backdrop" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div ref={panelRef} className={`modal modal-${size}`} role="dialog" aria-modal="true" aria-label={title}>
        <header className="modal-header">
          <div>
            <h2>{title}</h2>
            {description ? <p>{description}</p> : null}
          </div>
          <IconButton label="Close" onClick={onClose} className="subtle">
            <X size={18} />
          </IconButton>
        </header>
        <div className="modal-body">{children}</div>
        {footer ? <footer className="modal-footer">{footer}</footer> : null}
      </div>
    </div>,
    document.body,
  );
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  message,
  confirmLabel = "Confirm",
  danger = false,
  requireText,
  busy = false,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  requireText?: string;
  busy?: boolean;
}) {
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (open) setTyped("");
  }, [open]);
  const confirmed = !requireText || typed === requireText;
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      size="sm"
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant={danger ? "danger" : "primary"} onClick={onConfirm} disabled={!confirmed} loading={busy}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className={`confirm-message ${danger ? "danger" : ""}`}>
        {danger ? <AlertTriangle size={22} /> : null}
        <div>{message}</div>
      </div>
      {requireText ? (
        <Field label={`Type ${requireText} to confirm`}>
          <Input value={typed} onChange={(event) => setTyped(event.target.value)} autoComplete="off" />
        </Field>
      ) : null}
    </Modal>
  );
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  compact = false,
}: {
  icon?: ReactNode;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  compact?: boolean;
}) {
  return (
    <div className={`empty-state ${compact ? "compact" : ""}`}>
      {icon ? <div className="empty-icon">{icon}</div> : null}
      <h3>{title}</h3>
      {description ? <p>{description}</p> : null}
      {action ? <div className="empty-action">{action}</div> : null}
    </div>
  );
}

export function Spinner({ label = "Loading" }: { label?: string }) {
  return (
    <div className="spinner-row" role="status">
      <Loader2 size={18} className="spin" />
      <span>{label}</span>
    </div>
  );
}

export function LoadingBlock({ label = "Loading…" }: { label?: string }) {
  return (
    <div className="loading-block">
      <Spinner label={label} />
    </div>
  );
}

export function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="error-banner" role="alert">
      <AlertTriangle size={18} />
      <span>{message}</span>
      {onRetry ? (
        <Button size="sm" variant="ghost" onClick={onRetry}>
          Retry
        </Button>
      ) : null}
    </div>
  );
}

export function ProgressBar({ value, tone = "primary" }: { value: number; tone?: "primary" | "success" | "danger" }) {
  return (
    <div className={`progress progress-${tone}`} role="progressbar" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100}>
      <span style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  );
}

export function StatCard({
  label,
  value,
  detail,
  icon,
  tone = "neutral",
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  icon?: ReactNode;
  tone?: Tone;
}) {
  return (
    <div className="stat-card">
      <div className={`stat-icon stat-icon-${tone}`}>{icon}</div>
      <div className="stat-copy">
        <span className="stat-label">{label}</span>
        <strong className="stat-value">{value}</strong>
        {detail ? <span className="stat-detail">{detail}</span> : null}
      </div>
    </div>
  );
}

export function Tabs<T extends string>({
  tabs,
  active,
  onChange,
}: {
  tabs: { id: T; label: string; count?: number }[];
  active: T;
  onChange: (id: T) => void;
}) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          role="tab"
          aria-selected={active === tab.id}
          className={`tab ${active === tab.id ? "tab-active" : ""}`}
          onClick={() => onChange(tab.id)}
        >
          {tab.label}
          {tab.count !== undefined ? <span className="tab-count">{tab.count}</span> : null}
        </button>
      ))}
    </div>
  );
}

export function SearchInput({
  value,
  onChange,
  placeholder = "Search…",
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
}) {
  return (
    <div className="search-input">
      <Search size={16} />
      <input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} />
      {value ? (
        <button type="button" onClick={() => onChange("")} aria-label="Clear search">
          <X size={14} />
        </button>
      ) : null}
    </div>
  );
}

export function Pagination({
  page,
  perPage,
  total,
  onPage,
}: {
  page: number;
  perPage: number;
  total: number;
  onPage: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / perPage));
  if (pages <= 1) return null;
  return (
    <div className="pagination">
      <span>
        Page {page} of {pages}
      </span>
      <div>
        <IconButton label="Previous page" onClick={() => onPage(page - 1)} disabled={page <= 1}>
          <ChevronLeft size={16} />
        </IconButton>
        <IconButton label="Next page" onClick={() => onPage(page + 1)} disabled={page >= pages}>
          <ChevronRight size={16} />
        </IconButton>
      </div>
    </div>
  );
}

export function KeyValue({ items }: { items: { label: string; value: ReactNode; mono?: boolean }[] }) {
  return (
    <dl className="key-value">
      {items.map((item) => (
        <div key={item.label} className="key-value-row">
          <dt>{item.label}</dt>
          <dd className={item.mono ? "mono" : ""}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function CopyField({ value, label }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="copy-field">
      <code title={value}>{value}</code>
      <button
        type="button"
        aria-label={label ?? "Copy"}
        onClick={() => {
          void navigator.clipboard.writeText(value).then(() => {
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          });
        }}
      >
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </div>
  );
}

export function CodeBlock({ children, maxHeight = 420 }: { children: string; maxHeight?: number }) {
  return (
    <pre className="code-block" style={{ maxHeight }}>
      <code>{children}</code>
    </pre>
  );
}

export function Notice({
  tone = "info",
  title,
  children,
}: {
  tone?: "info" | "warning" | "danger" | "success";
  title?: string;
  children: ReactNode;
}) {
  return (
    <div className={`notice notice-${tone}`}>
      <AlertTriangle size={17} />
      <div>
        {title ? <strong>{title}</strong> : null}
        <div>{children}</div>
      </div>
    </div>
  );
}
