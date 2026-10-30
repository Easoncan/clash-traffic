import { describe, expect, test } from "bun:test";
import { takeChanged } from "../src/lib/form";
import { setLang } from "../src/lib/i18n";
import { appendRate, newerLive } from "../src/lib/live";
import { processState } from "../src/lib/process";
import { rangeLabel, resolve } from "../src/lib/range";
import type { ClashStatus, Live } from "../src/mygo";

describe("actual process identification state", () => {
  const checked: ClashStatus = { connected: true, endpoint: "test", source: "test", version: "test", error: "", findProcessMode: "always", processCheckedAt: 1, processCheckError: "", processConnections: 2, processIdentified: 1, processSupplemented: 0, processSampleError: "", since: 1 };
  test("uses the actual mode rather than the maintenance checkbox", () => {
    expect(processState(checked)).toBe("enabled");
    expect(processState({ ...checked, findProcessMode: "strict" })).toBe("partial");
    expect(processState({ ...checked, findProcessMode: "off" })).toBe("disabled");
  });
  test("an old or failed check is not displayed as enabled", () => {
    expect(processState({ ...checked, connected: false })).toBe("disconnected");
    expect(processState({ ...checked, processCheckedAt: 0 })).toBe("waiting");
    expect(processState({ ...checked, processCheckError: "cannot verify" })).toBe("error");
    expect(processState({ ...checked, findProcessMode: "unsupported" })).toBe("unknown");
  });
  test("no traffic or unavailable samples do not mean the runtime mode is off", () => {
    expect(processState({ ...checked, processConnections: 0, processIdentified: 0 })).toBe("enabled");
    expect(processState({ ...checked, processSampleError: "cannot observe" })).toBe("enabled");
  });
});

const live = (seq: number, n: number, run = "r1", t = seq): Live => ({ t, upRate: 0, downRate: 0, coreUp: 0, coreDown: 0, conns: [], total: n, run, seq });

describe("newerLive (review B10)", () => {
  test("a late reply does not replace a newer event", () => {
    const event = live(2000, 300);
    const lateReply = live(1000, 500);
    expect(newerLive(event, lateReply)).toBe(event);
  });
  test("a newer snapshot replaces an older one", () => {
    expect(newerLive(live(1000, 1), live(2000, 2)).seq).toBe(2000);
    expect(newerLive(null, live(5, 1)).seq).toBe(5);
  });
  test("the clock going back does not stop the page (review R8)", () => {
    const before = live(10, 1, "r1", 5_000_000);
    const after = live(11, 1, "r1", 1_000_000); // an hour earlier by the clock
    expect(newerLive(before, after)).toBe(after);
  });
  test("a snapshot of a new run replaces the old run's", () => {
    expect(newerLive(live(500, 1, "old"), live(1, 1, "new")).run).toBe("new");
  });
});

describe("custom range (review #18)", () => {
  test("both days count, in either order", () => {
    for (const [from, to] of [
      ["2026-10-12", "2026-10-14"],
      ["2026-10-14", "2026-10-12"],
    ]) {
      const r = { preset: "custom" as const, from: from!, to: to! };
      const { from: a, to: b } = resolve(r);
      expect(new Date(a).getDate()).toBe(12);
      expect((b - a) / 86_400_000).toBe(3);
      setLang("zh-Hans");
      expect(rangeLabel(r)).toBe("2026-10-12 至 2026-10-14");
      setLang("en");
      expect(rangeLabel(r)).toBe("2026-10-12 to 2026-10-14");
    }
  });
});

describe("appendRate (round four, B4)", () => {
  const empty = { rates: [], run: "", seq: 0 };
  test("each snapshot once, in order", () => {
    let st = appendRate(empty, live(1, 0, "r", 1000));
    st = appendRate(st, live(2, 0, "r", 2000));
    st = appendRate(st, live(2, 0, "r", 2000)); // again
    st = appendRate(st, live(1, 0, "r", 1000)); // late
    expect(st.rates.map((p) => p.t)).toEqual([1000, 2000]);
  });
  test("the clock going back starts the chart over", () => {
    let st = appendRate(empty, live(1, 0, "r", 5_000_000));
    st = appendRate(st, live(2, 0, "r", 5_001_000));
    st = appendRate(st, live(3, 0, "r", 1_000_000)); // an hour earlier
    st = appendRate(st, live(4, 0, "r", 1_001_000));
    expect(st.rates.map((p) => p.t)).toEqual([1_000_000, 1_001_000]);
  });
  test("keeps the last points only", () => {
    let st = empty as ReturnType<typeof appendRate>;
    for (let i = 1; i <= 10; i++) st = appendRate(st, live(i, 0, "r", i * 1000), 4);
    expect(st.rates.length).toBe(4);
    expect(st.rates[0]!.t).toBe(7000);
  });
  test("clearing storage starts a fresh chart even when time moves forward", () => {
    const before = appendRate(empty, live(10, 0, "before-clear", 1000));
    const after = appendRate(before, live(1, 0, "after-clear", 2000));
    expect(after.rates.map((p) => p.t)).toEqual([2000]);
  });
  test("initial history stays when the first run is learned", () => {
    const loaded = { rates: [{ t: 1000, up: 1, down: 2 }], run: "", seq: 0 };
    expect(appendRate(loaded, live(1, 0, "current", 2000)).rates.map((p) => p.t)).toEqual([1000, 2000]);
  });
});

describe("takeChanged (review 7, P2-3)", () => {
  const saved = { autoCheckUpdates: true, autoInstallUpdates: false, deviceName: "Mac" };
  test("an unsaved edit outlives a check for updates", () => {
    const form = { ...saved, autoInstallUpdates: true, deviceName: "MacBook" };
    // A check sends the preferences as they are, unchanged.
    expect(takeChanged(form, saved, { autoCheckUpdates: true, autoInstallUpdates: false })).toEqual(form);
  });
  test("a change from the update window shows in fields left alone", () => {
    const form = { ...saved, deviceName: "MacBook" };
    expect(takeChanged(form, saved, { autoCheckUpdates: true, autoInstallUpdates: true })).toEqual({ ...form, autoInstallUpdates: true });
  });
  test("an edited field keeps the edit, the others follow", () => {
    const form = { ...saved, autoCheckUpdates: false };
    expect(takeChanged(form, saved, { autoCheckUpdates: true, autoInstallUpdates: true })).toEqual({ ...form, autoInstallUpdates: true });
  });
});
