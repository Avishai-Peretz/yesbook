const timestampFormat = new Intl.DateTimeFormat("en-US", {
  dateStyle: "medium",
  timeStyle: "short",
  timeZone: "UTC",
});

const costFormat = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  minimumFractionDigits: 2,
  maximumFractionDigits: 6,
});

export function formatTimestamp(value: string): string {
  return `${timestampFormat.format(new Date(value))} UTC`;
}

export function formatBytes(size: number): string {
  if (size < 1024) return `${String(size)} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

export function formatCostMicros(micros: number): string {
  return costFormat.format(micros / 1_000_000);
}

export function formatCoverage(coverage: number): string {
  return `${String(Math.round(coverage * 100))}%`;
}
