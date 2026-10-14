import { isoDate } from "./format";

export type Preset = "today" | "yesterday" | "7d" | "30d" | "month" | "custom";

export const PRESETS: { id: Preset; label: string }[] = [
  { id: "today", label: "今天" },
  { id: "yesterday", label: "昨天" },
  { id: "7d", label: "近 7 天" },
  { id: "30d", label: "近 30 天" },
  { id: "month", label: "本月" },
  { id: "custom", label: "自定义" },
];

export interface Range {
  preset: Preset;
  /** yyyy-mm-dd, for custom ranges, both days included */
  from: string;
  to: string;
}

function midnight(d = new Date()): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

function addDays(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n);
}

function parseDay(s: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null;
}

/** The milliseconds a range covers, [from, to). */
export function resolve(r: Range): { from: number; to: number; live: boolean } {
  const today = midnight();
  const tomorrow = addDays(today, 1);
  switch (r.preset) {
    case "today":
      return { from: today.getTime(), to: tomorrow.getTime(), live: true };
    case "yesterday":
      return { from: addDays(today, -1).getTime(), to: today.getTime(), live: false };
    case "7d":
      return { from: addDays(today, -6).getTime(), to: tomorrow.getTime(), live: true };
    case "30d":
      return { from: addDays(today, -29).getTime(), to: tomorrow.getTime(), live: true };
    case "month":
      return { from: new Date(today.getFullYear(), today.getMonth(), 1).getTime(), to: tomorrow.getTime(), live: true };
    case "custom": {
      // Both days count, whichever order they were picked in.
      const [first, last] = ordered(r);
      const end = addDays(last, 1);
      return { from: first.getTime(), to: end.getTime(), live: end > new Date() };
    }
  }
}

function ordered(r: Range): [Date, Date] {
  const today = midnight();
  const a = parseDay(r.from) ?? today;
  const b = parseDay(r.to) ?? today;
  return a <= b ? [a, b] : [b, a];
}

export function defaultRange(): Range {
  const today = isoDate(Date.now());
  return { preset: "today", from: isoDate(addDays(midnight(), -6).getTime()), to: today };
}

export function rangeLabel(r: Range): string {
  if (r.preset === "custom") {
    const [a, b] = ordered(r);
    return `${isoDate(a.getTime())} 至 ${isoDate(b.getTime())}`;
  }
  return PRESETS.find((p) => p.id === r.preset)?.label ?? "";
}
