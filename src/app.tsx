import { useCallback, useEffect, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { events, Monitor, Sync, Traffic, type ClashStatus, type DeviceInfo, type MeshStatus, type Query, type SyncStatus } from "./mygo";
import { PRESETS, defaultRange, resolve, type Preset, type Range } from "./lib/range";
import { ago, isoDate } from "./lib/format";
import { useEventValue } from "./lib/hooks";
import { Overview } from "./pages/overview";
import { Ranking } from "./pages/ranking";
import { LivePage } from "./pages/live";
import { DevicesPage } from "./pages/devices";
import { SettingsPage } from "./pages/settings";
import { RecordsPage } from "./pages/records";
import { SubsPage } from "./pages/subs";

export type PageId = "overview" | "apps" | "hosts" | "records" | "live" | "subs" | "devices" | "settings";

/** Which traffic the reports count: all of it, the proxied, or the direct. */
export type RouteFilter = "" | "proxy" | "direct";

export const ROUTE_LABEL: Record<string, string> = { proxy: "代理", direct: "直连", reject: "拦截", unknown: "未知" };

/** How a subscription reads; "" is the traffic of none. */
export function subLabel(sub: string): string {
  return sub === "" ? "直连（不经订阅）" : sub;
}

const NAV: { id: PageId; label: string; icon: string }[] = [
  { id: "overview", label: "概览", icon: "M3 13h4v8H3zM10 8h4v13h-4zM17 3h4v18h-4z" },
  { id: "apps", label: "应用", icon: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z" },
  { id: "hosts", label: "访问地址", icon: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18" },
  { id: "records", label: "访问记录", icon: "M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01" },
  { id: "live", label: "实时连接", icon: "M3 12h4l3-8 4 16 3-8h4" },
  { id: "subs", label: "订阅", icon: "M4 7h16v12H4zM8 7V5h8v2M4 12h16" },
  { id: "devices", label: "设备", icon: "M3 5h18v11H3zM8 20h8M12 16v4" },
  { id: "settings", label: "设置", icon: "M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1" },
];

/** What every report of the app is scoped to. */
export interface Filters {
  range: Range;
  device: string; // "" for every device
  app: string;
  host: string;
  chain: string;
  route: RouteFilter;
  /** a subscription; "" for the traffic of none, null for all */
  sub: string | null;
  groupDomain: boolean;
}

export interface PageProps {
  filters: Filters;
  setFilters: (f: Partial<Filters>) => void;
  go: (page: PageId) => void;
  notify: (msg: string, kind?: "ok" | "error") => void;
  status: ClashStatus | null;
}

/** The query of the filters. */
export function toQuery(f: Filters, extra: Partial<Query> = {}): { query: Query; live: boolean } {
  const { from, to, live } = resolve(f.range);
  return {
    live,
    query: {
      from,
      to,
      devices: f.device ? [f.device] : [],
      app: f.app,
      host: f.host,
      chain: f.chain,
      groupDomain: f.groupDomain,
      search: "",
      limit: 100,
      bucket: "",
      route: f.route,
      sub: f.sub ?? "",
      noSub: f.sub === "",
      ...extra,
    },
  };
}

// The route filter is remembered: someone counting a proxy plan's traffic
// wants it every time.
function loadRoute(): RouteFilter {
  try {
    const v = localStorage.getItem("route");
    return v === "proxy" || v === "direct" ? v : "";
  } catch {
    return "";
  }
}

function saveRoute(v: RouteFilter) {
  try {
    localStorage.setItem("route", v);
  } catch {}
}

/** All traffic, only the proxied, or only the direct. */
export function RouteSwitch({ value, onChange }: { value: RouteFilter; onChange: (v: RouteFilter) => void }) {
  const options: [RouteFilter, string][] = [
    ["", "全部流量"],
    ["proxy", "仅代理"],
    ["direct", "仅直连"],
  ];
  return (
    <div class="segmented" role="radiogroup" aria-label="路由">
      {options.map(([v, label]) => (
        <button type="button" key={v} role="radio" aria-checked={value === v} class={value === v ? "on" : ""} onClick={() => onChange(v)}>
          {label}
        </button>
      ))}
    </div>
  );
}

function Icon({ d }: { d: string }) {
  return (
    <svg class="icon" viewBox="0 0 24 24" aria-hidden="true">
      <path d={d} />
    </svg>
  );
}

export function App() {
  const [page, setPage] = useState<PageId>(() => (localStorage.getItem("page") as PageId) || "overview");
  const [filters, setFiltersState] = useState<Filters>(() => ({
    range: defaultRange(),
    device: "",
    app: "",
    host: "",
    chain: "",
    route: loadRoute(),
    sub: null,
    groupDomain: false,
  }));
  const [toast, setToast] = useState<{ msg: string; kind: "ok" | "error"; id: number } | null>(null);
  const status = useEventValue(() => Monitor.status(), (fn) => events.status.on(fn));
  const sync = useEventValue<SyncStatus>(() => Sync.status(), (fn) => events.sync.on(fn));
  const meshStatus = useEventValue<MeshStatus>(() => Monitor.mesh(), (fn) => events.mesh.on(fn));
  const meshPeers = meshStatus?.peers.filter((p) => p.state === "app").length ?? 0;

  const go = useCallback((p: PageId) => {
    setPage(p);
    try {
      localStorage.setItem("page", p);
    } catch {}
  }, []);
  const setFilters = useCallback((f: Partial<Filters>) => {
    if (f.route !== undefined) saveRoute(f.route);
    setFiltersState((cur) => ({ ...cur, ...f }));
  }, []);
  const notify = useCallback((msg: string, kind: "ok" | "error" = "ok") => setToast({ msg, kind, id: Date.now() }), []);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), toast.kind === "error" ? 6000 : 3000);
    return () => clearTimeout(t);
  }, [toast]);

  const props: PageProps = { filters, setFilters, go, notify, status };
  const current = NAV.find((n) => n.id === page)!;

  return (
    <div class="shell">
      <aside class="sidebar">
        <div class="brand">
          <span class="brand-mark" aria-hidden="true">
            <Icon d="M7 20V5M3 9l4-4 4 4M17 4v15M13 15l4 4 4-4" />
          </span>
          Clash Traffic
        </div>
        <nav>
          {NAV.map((n) => (
            <button type="button" key={n.id} class={`nav-item${n.id === page ? " active" : ""}`} onClick={() => go(n.id)} aria-current={n.id === page ? "page" : undefined}>
              <Icon d={n.icon} />
              {n.label}
            </button>
          ))}
        </nav>
        <div class="sidebar-foot">
          <button type="button" class="status-line" onClick={() => go("settings")} title={status?.error || status?.endpoint || ""}>
            <span class={`status-dot ${status?.connected ? "good" : "critical"}`} aria-hidden="true" />
            <span>
              {status?.connected ? "已连接 Clash" : "未连接 Clash"}
              <small>{status?.connected ? status.version : "点击查看原因"}</small>
            </span>
          </button>
          {meshStatus?.enabled && (
            <button type="button" class="status-line" onClick={() => go("devices")} title={meshStatus.error || meshStatus.serveError}>
              <span class={`status-dot ${meshStatus.available && meshStatus.serving ? "good" : meshStatus.available ? "warning" : "critical"}`} aria-hidden="true" />
              <span>
                {meshStatus.available ? "Tailscale 共享" : "Tailscale 未就绪"}
                <small>{meshStatus.available ? `发现 ${meshPeers} 台设备` : meshStatus.error || "正在查找…"}</small>
              </span>
            </button>
          )}
          {sync && sync.kind !== "off" && (
            <button type="button" class="status-line" onClick={() => go("devices")} title={sync.lastError}>
              <span class={`status-dot ${sync.running ? "info" : sync.lastError ? "warning" : "good"}`} aria-hidden="true" />
              <span>
                {sync.running ? "正在同步…" : sync.lastError ? "同步出错" : "多设备同步"}
                <small>{sync.lastSync ? `上次 ${ago(sync.lastSync)}` : "尚未同步"}</small>
              </span>
            </button>
          )}
        </div>
      </aside>
      <main class="main">
        <header class="titlebar">
          <h1>{current.label}</h1>
        </header>
        <div class="content">
          {page === "overview" && <Overview {...props} />}
          {page === "apps" && <Ranking kind="apps" {...props} />}
          {page === "hosts" && <Ranking kind="hosts" {...props} />}
          {page === "records" && <RecordsPage {...props} />}
          {page === "live" && <LivePage {...props} />}
          {page === "subs" && <SubsPage {...props} />}
          {page === "devices" && <DevicesPage {...props} />}
          {page === "settings" && <SettingsPage {...props} />}
        </div>
      </main>
      {toast && (
        <div class={`toast ${toast.kind}`} role="status" key={toast.id}>
          {toast.msg}
        </div>
      )}
    </div>
  );
}

/** The row of filters above a page's reports. */
export function FilterBar({ filters, setFilters, children, hide = [] }: Pick<PageProps, "filters" | "setFilters"> & { children?: ComponentChildren; hide?: ("app" | "host" | "chain" | "device" | "sub")[] }) {
  const [devices, setDevices] = useState<DeviceInfo[]>([]);
  useEffect(() => {
    const load = () => Traffic.devices().then(setDevices).catch(() => {});
    void load();
    return events.data.on(() => void load());
  }, []);
  const r = filters.range;
  const setPreset = (preset: Preset) => setFilters({ range: { ...r, preset } });
  const chip = (label: string, value: string, clear: Partial<Filters>) =>
    value && (
      <span class="chip">
        <span class="muted">{label}</span>
        <span class="chip-value">{value}</span>
        <button type="button" aria-label={`清除${label}筛选`} onClick={() => setFilters(clear)}>
          ×
        </button>
      </span>
    );

  return (
    <div class="filterbar">
      <div class="segmented" role="radiogroup" aria-label="时间范围">
        {PRESETS.map((p) => (
          <button type="button" key={p.id} role="radio" aria-checked={r.preset === p.id} class={r.preset === p.id ? "on" : ""} onClick={() => setPreset(p.id)}>
            {p.label}
          </button>
        ))}
      </div>
      {r.preset === "custom" && (
        <span class="dates">
          <input type="date" value={r.from} max={isoDate(Date.now())} onChange={(e) => setFilters({ range: { ...r, from: e.currentTarget.value } })} />
          <span class="muted">至</span>
          <input type="date" value={r.to} max={isoDate(Date.now())} onChange={(e) => setFilters({ range: { ...r, to: e.currentTarget.value } })} />
        </span>
      )}
      {!hide.includes("device") && devices.length > 1 && (
        <select value={filters.device} onChange={(e) => setFilters({ device: e.currentTarget.value })} aria-label="设备">
          <option value="">全部设备（{devices.length}）</option>
          {devices.map((d, i) => (
            <option key={d.id} value={d.id}>
              {d.name}
              {i === 0 ? "（本机）" : ""}
            </option>
          ))}
        </select>
      )}
      <RouteSwitch value={filters.route} onChange={(route) => setFilters({ route })} />
      {!hide.includes("app") && chip("应用", filters.app, { app: "" })}
      {!hide.includes("host") && chip(filters.groupDomain ? "域名" : "地址", filters.host, { host: "" })}
      {!hide.includes("chain") && chip("节点", filters.chain, { chain: "" })}
      {!hide.includes("sub") && filters.sub !== null && chip("订阅", subLabel(filters.sub), { sub: null })}
      <span class="spacer" />
      {children}
    </div>
  );
}

export function Card({ title, extra, children, class: cls }: { title?: ComponentChildren; extra?: ComponentChildren; children: ComponentChildren; class?: string }) {
  return (
    <section class={`card${cls ? " " + cls : ""}`}>
      {(title || extra) && (
        <header class="card-head">
          <h2>{title}</h2>
          {extra}
        </header>
      )}
      {children}
    </section>
  );
}
