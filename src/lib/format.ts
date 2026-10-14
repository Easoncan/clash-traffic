import { lang, t } from "./i18n";

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/** Bytes in binary units: 1.23 GB, 45.6 MB, 512 B. */
export function bytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  let i = 0;
  let v = n;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  if (i === 0) return `${Math.round(v)} B`;
  const digits = v < 10 ? 2 : v < 100 ? 1 : 0;
  return `${v.toFixed(digits)} ${UNITS[i]}`;
}

/** A speed: 1.2 MB/s. */
export function rate(n: number): string {
  return `${bytes(n)}/s`;
}

/** A count: 1,284 · 129K · 12.9万. */
export function count(n: number): string {
  if (n >= 100_000) return new Intl.NumberFormat(lang(), { notation: "compact", maximumFractionDigits: 1 }).format(n);
  return n.toLocaleString(lang());
}

/** A share of a total: 12.3%. */
export function percent(part: number, total: number): string {
  if (total <= 0) return "0%";
  const p = (part / total) * 100;
  if (p > 0 && p < 0.1) return "<0.1%";
  return `${p.toFixed(p < 10 ? 1 : 0)}%`;
}

/** A duration: 3 s, 12 min 5 s, 2 h 3 min, 1 d 4 h. */
export function duration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return t("{s} s", { s });
  const m = Math.floor(s / 60);
  if (m < 60) return t("{m} min {s} s", { m, s: s % 60 });
  const h = Math.floor(m / 60);
  if (h < 24) return t("{h} h {m} min", { h, m: m % 60 });
  return t("{d} d {h} h", { d: Math.floor(h / 24), h: h % 24 });
}

/** How long ago: just now, 5 minutes ago, 3 hours ago, 10/12 14:03. */
export function ago(ms: number): string {
  if (!ms) return t("Never");
  const d = Date.now() - ms;
  if (d < 60_000) return t("Just now");
  const rtf = new Intl.RelativeTimeFormat(lang(), { numeric: "always" });
  if (d < 3_600_000) return rtf.format(-Math.floor(d / 60_000), "minute");
  if (d < 86_400_000) return rtf.format(-Math.floor(d / 3_600_000), "hour");
  return dateTime(ms);
}

const pad = (n: number) => String(n).padStart(2, "0");

/** A day and time: 10/12 14:03, in the order of the language. */
export function dateTime(ms: number): string {
  return new Intl.DateTimeFormat(lang(), { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(ms);
}

export function hourLabel(ms: number): string {
  return `${pad(new Date(ms).getHours())}:00`;
}

export function dayLabel(ms: number): string {
  return new Intl.DateTimeFormat(lang(), { month: "numeric", day: "numeric" }).format(ms);
}

export function clock(ms: number): string {
  const d = new Date(ms);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** yyyy-mm-dd of a local date, for <input type="date">. */
export function isoDate(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

const OS: Record<string, string> = { darwin: "macOS", windows: "Windows", linux: "Linux" };
export function osName(os: string): string {
  return OS[os] ?? os;
}

/** Nice round ticks from 0 to at least max, in binary units. */
export function byteTicks(max: number, n = 4): number[] {
  if (max <= 0) return [0];
  const raw = max / n;
  const unit = 1024 ** Math.max(0, Math.floor(Math.log(raw) / Math.log(1024)));
  const scaled = raw / unit;
  const step = ([1, 2, 2.5, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000].find((s) => s >= scaled) ?? 1000) * unit;
  const ticks: number[] = [];
  for (let v = 0; v <= max + step * 0.999; v += step) ticks.push(v);
  if (ticks.length < 2) ticks.push(step);
  return ticks;
}
