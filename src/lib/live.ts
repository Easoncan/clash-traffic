import type { Live, RatePoint } from "../mygo";

/**
 * The later of two snapshots: a full snapshot fetched a moment ago must not
 * replace a newer live event that arrived before it. Snapshots are ordered
 * by their number within a run of the app, not by the clock, which may be
 * set back; a snapshot of another run, after the app restarted, is newer.
 */
export function newerLive(cur: Live | null, next: Live): Live {
  if (!cur || cur.run !== next.run) return next;
  return cur.seq > next.seq ? cur : next;
}

/** The speed chart's points, and the last snapshot they took. */
export interface RateState {
  rates: RatePoint[];
  run: string;
  seq: number;
}

/**
 * Adds a snapshot's speed to the chart, once. The chart's axis is the
 * clock: when the clock went back, the chart starts over rather than stop.
 */
export function appendRate(st: RateState, l: Live, max = 300): RateState {
  if (st.run === l.run && l.seq <= st.seq) return st;
  const point = { t: l.t, up: l.upRate, down: l.downRate };
  const last = st.rates[st.rates.length - 1];
  const rates = last && l.t <= last.t ? [point] : [...st.rates, point];
  return { rates: rates.length > max ? rates.slice(rates.length - max) : rates, run: l.run, seq: l.seq };
}
