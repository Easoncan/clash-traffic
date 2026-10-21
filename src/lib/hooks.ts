import { useEffect, useRef, useState } from "preact/hooks";
import type { RefObject } from "preact";
import { isCallError } from "mygo-runtime";
import { events, Traffic, type Query, type Report } from "../mygo";

/** The message of an error a call rejected with. */
export function errorText(err: unknown): string {
  if (isCallError(err)) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}

/** The width of an element, following its resizes. */
export function useWidth<T extends Element>(): [RefObject<T>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => {
      if (entry) setWidth(Math.floor(entry.contentRect.width));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

/**
 * The report of a query. It refreshes every few seconds while the range
 * reaches the present, and whenever data of other devices arrives. While the
 * report of a new query loads, the last one stays, and loading is true;
 * refreshes leave loading alone, or the page would dim every few seconds.
 */
export function useReport(query: Query, live: boolean): { report: Report | null; loading: boolean; error: string } {
  const [report, setReport] = useState<Report | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const key = JSON.stringify(query);
  const seq = useRef(0);

  useEffect(() => {
    let alive = true;
    const load = async (fresh = false) => {
      const n = ++seq.current;
      if (fresh) setLoading(true);
      try {
        const r = await Traffic.report(query);
        if (alive && n === seq.current) {
          setReport(r);
          setError("");
        }
      } catch (err) {
        if (alive && n === seq.current) setError(errorText(err));
      } finally {
        if (alive && n === seq.current) setLoading(false);
      }
    };
    void load(true);
    const timer = live ? setInterval(load, 5000) : undefined;
    const off = events.data.on(() => void load());
    return () => {
      alive = false;
      if (timer) clearInterval(timer);
      off();
    };
  }, [key, live]);

  return { report, loading, error };
}

/** The latest value of an event, starting from what initial returns. */
export function useEventValue<T>(initial: () => Promise<T>, subscribe: (fn: (v: T) => void) => () => void): T | null {
  const [value, setValue] = useState<T | null>(null);
  useEffect(() => {
    let alive = true;
    initial()
      .then((v) => alive && setValue((cur) => cur ?? v))
      .catch(() => {});
    const off = subscribe((v) => setValue(v));
    return () => {
      alive = false;
      off();
    };
  }, []);
  return value;
}
