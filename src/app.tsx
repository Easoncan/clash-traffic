import { useCallback, useEffect, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { events, Monitor, Sync, Traffic, type ClashStatus, type DeviceInfo, type MeshStatus, type Query, type SyncStatus } from "./mygo";
import { PRESETS, defaultRange, resolve, type Preset, type Range } from "./lib/range";
import { ago, isoDate } from "./lib/format";
import { useEventValue } from "./lib/hooks";
import { lang, setLang, t, tn, type Lang } from "./lib/i18n";
import { nameLabel, subLabel } from "./lib/names";
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

export { nameLabel, routeLabel, subLabel } from "./lib/names";

/** The pages, their names in English: t() shows them. */
const NAV: { id: PageId; label: string; icon: string }[] = [
  { id: "overview", label: "Overview", icon: "M3 13h4v8H3zM10 8h4v13h-4zM17 3h4v18h-4z" },
  { id: "apps", label: "Apps", icon: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z" },
  { id: "hosts", label: "Hosts", icon: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18" },
  { id: "records", label: "Access log", icon: "M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01" },
  { id: "live", label: "Live", icon: "M3 12h4l3-8 4 16 3-8h4" },
  { id: "subs", label: "Subscriptions", icon: "M4 7h16v12H4zM8 7V5h8v2M4 12h16" },
  { id: "devices", label: "Devices", icon: "M3 5h18v11H3zM8 20h8M12 16v4" },
  { id: "settings", label: "Settings", icon: "M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1" },
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
      searchAlso: [],
      searchIn: "",
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
    ["", "All traffic"],
    ["proxy", "Proxy only"],
    ["direct", "Direct only"],
  ];
  return (
    <div class="segmented" role="radiogroup" aria-label={t("Route")}>
      {options.map(([v, label]) => (
        <button type="button" key={v} role="radio" aria-checked={value === v} class={value === v ? "on" : ""} onClick={() => onChange(v)}>
          {t(label)}
        </button>
      ))}
    </div>
  );
}

/** The app's icon: Clash's cat, its eyes an up and a down arrow. */
function BrandMark() {
  return (
    <svg class="brand-mark" viewBox="0 0 100 100" aria-hidden="true">
      <defs>
        <linearGradient id="brand-gradient" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="45" y2="100">
          <stop offset="0" stop-color="#ff7eb3" />
          <stop offset="0.5" stop-color="#9b6bf2" />
          <stop offset="1" stop-color="#3f82f0" />
        </linearGradient>
      </defs>
      <rect width="100" height="100" rx="22" fill="url(#brand-gradient)" />
      <g fill="#fff" stroke="#fff" stroke-width="4.5" stroke-linejoin="round">
        <ellipse cx="50" cy="55" rx="22.5" ry="17.6" />
        <polygon points="27.8,48.6 46.2,37.8 30.8,23.6" />
        <polygon points="72.2,48.6 53.8,37.8 69.2,23.6" />
      </g>
      <g fill="url(#brand-gradient)">
        <polygon points="41.25,49.6 37,55 45.5,55" />
        <rect x="39.8" y="54.3" width="2.9" height="6.8" rx="0.8" />
        <polygon points="58.75,60.4 54.5,55 63,55" />
        <rect x="57.3" y="48.9" width="2.9" height="6.8" rx="0.8" />
        <polygon points="47.6,63 52.4,63 50,65.8" />
      </g>
    </svg>
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
  // The page saved may be one another version had.
  const [page, setPage] = useState<PageId>(() => NAV.find((n) => n.id === localStorage.getItem("page"))?.id ?? "overview");
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
  // Every page shows in the language the settings chose, at once.
  const [, setShown] = useState<Lang>(lang());
  useEffect(
    () =>
      events.language.on((l) => {
        setLang(l as Lang);
        setShown(l as Lang);
      }),
    [],
  );

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
  useEffect(() => events.data.on((change) => {
    if (change.reason === "clear") {
      setFiltersState((cur) => ({ ...cur, device: "", app: "", host: "", chain: "", sub: null }));
    }
  }), []);
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
          <BrandMark />
          Clash Traffic
        </div>
        <nav>
          {NAV.map((n) => (
            <button type="button" key={n.id} class={`nav-item${n.id === page ? " active" : ""}`} onClick={() => go(n.id)} aria-current={n.id === page ? "page" : undefined}>
              <Icon d={n.icon} />
              {t(n.label)}
            </button>
          ))}
        </nav>
        <div class="sidebar-foot">
          <button type="button" class="status-line" onClick={() => go("settings")} title={status?.error || status?.endpoint || ""}>
            <span class={`status-dot ${status?.connected ? "good" : "critical"}`} aria-hidden="true" />
            <span>
              {status?.connected ? t("Connected to Clash") : t("Not connected to Clash")}
              <small>{status?.connected ? status.version : t("Click to see why")}</small>
            </span>
          </button>
          {meshStatus?.enabled && (
            <button type="button" class="status-line" onClick={() => go("devices")} title={meshStatus.error || meshStatus.serveError}>
              <span class={`status-dot ${meshStatus.available && meshStatus.serving ? "good" : meshStatus.available ? "warning" : "critical"}`} aria-hidden="true" />
              <span>
                {meshStatus.available ? t("Tailscale sharing") : t("Tailscale not ready")}
                <small>{meshStatus.available ? tn("{n} devices found", meshPeers) : meshStatus.error || t("Searching…")}</small>
              </span>
            </button>
          )}
          {sync && sync.kind !== "off" && (
            <button type="button" class="status-line" onClick={() => go("devices")} title={sync.lastError}>
              <span class={`status-dot ${sync.running ? "info" : sync.lastError ? "warning" : "good"}`} aria-hidden="true" />
              <span>
                {sync.running ? t("Syncing…") : sync.lastError ? t("Sync failed") : t("Multi-device sync")}
                <small>{sync.lastSync ? t("Last synced {ago}", { ago: ago(sync.lastSync) }) : t("Not synced yet")}</small>
              </span>
            </button>
          )}
        </div>
      </aside>
      <main class="main">
        <header class="titlebar">
          <h1>{t(current.label)}</h1>
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
        <button type="button" aria-label={t("Clear the {filter} filter", { filter: label })} onClick={() => setFilters(clear)}>
          ×
        </button>
      </span>
    );

  return (
    <div class="filterbar">
      <div class="segmented" role="radiogroup" aria-label={t("Time range")}>
        {PRESETS.map((p) => (
          <button type="button" key={p.id} role="radio" aria-checked={r.preset === p.id} class={r.preset === p.id ? "on" : ""} onClick={() => setPreset(p.id)}>
            {t(p.label)}
          </button>
        ))}
      </div>
      {r.preset === "custom" && (
        <span class="dates">
          <input type="date" value={r.from} max={isoDate(Date.now())} onChange={(e) => setFilters({ range: { ...r, from: e.currentTarget.value } })} />
          <span class="muted">{t("to")}</span>
          <input type="date" value={r.to} max={isoDate(Date.now())} onChange={(e) => setFilters({ range: { ...r, to: e.currentTarget.value } })} />
        </span>
      )}
      {!hide.includes("device") && devices.length > 1 && (
        <select value={filters.device} onChange={(e) => setFilters({ device: e.currentTarget.value })} aria-label={t("Device")}>
          <option value="">{t("All devices ({n})", { n: devices.length })}</option>
          {devices.map((d, i) => (
            <option key={d.id} value={d.id}>
              {d.name}
              {i === 0 ? t(" (this computer)") : ""}
            </option>
          ))}
        </select>
      )}
      <RouteSwitch value={filters.route} onChange={(route) => setFilters({ route })} />
      {!hide.includes("app") && chip(t("App"), nameLabel(filters.app), { app: "" })}
      {!hide.includes("host") && chip(filters.groupDomain ? t("Domain") : t("Host"), nameLabel(filters.host), { host: "" })}
      {!hide.includes("chain") && chip(t("Node"), filters.chain, { chain: "" })}
      {!hide.includes("sub") && filters.sub !== null && chip(t("Subscription"), subLabel(filters.sub), { sub: null })}
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
