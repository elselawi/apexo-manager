import type { ReactNode } from "react";

export function Sparkline({
  values,
  height = 52,
  stroke = "var(--primary)",
  fill = "rgba(13, 148, 136, .12)",
}: {
  values: number[];
  height?: number;
  stroke?: string;
  fill?: string;
}) {
  if (values.length < 2) {
    return <div className="sparkline-empty" style={{ height }} />;
  }
  const width = 320;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const points = values.map((value, index) => {
    const x = (index / (values.length - 1)) * width;
    const y = height - 4 - ((value - min) / span) * (height - 8);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const path = `M ${points.join(" L ")}`;
  const area = `${path} L ${width},${height} L 0,${height} Z`;
  return (
    <svg className="sparkline" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label="Trend">
      <path d={area} fill={fill} stroke="none" />
      <path d={path} fill="none" stroke={stroke} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

export function Gauge({
  value,
  label,
  detail,
  tone = "primary",
}: {
  value: number;
  label: string;
  detail?: ReactNode;
  tone?: "primary" | "warning" | "danger";
}) {
  const clamped = Math.max(0, Math.min(100, value));
  return (
    <div className="gauge-card">
      <div className={`gauge gauge-${tone}`} style={{ "--gauge-value": `${clamped * 3.6}deg` } as React.CSSProperties}>
        <div className="gauge-inner">
          <strong>{clamped.toFixed(0)}%</strong>
        </div>
      </div>
      <div>
        <span className="stat-label">{label}</span>
        <div className="gauge-detail">{detail}</div>
      </div>
    </div>
  );
}
