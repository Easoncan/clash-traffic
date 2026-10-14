import { useState } from "preact/hooks";
import { Card, FilterBar, toQuery, type PageProps } from "../app";
import { errorText, useEventValue, useReport } from "../lib/hooks";
import { ago, bytes, count, osName, percent, rate } from "../lib/format";
import { t, tn } from "../lib/i18n";
import { subLabel } from "../lib/names";
import { events, Monitor, Sync, Traffic, type MeshStatus, type PeerStatus, type SyncStatus } from "../mygo";

/** The ways to sync, in English: t() shows them. */
const KIND: Record<string, string> = { off: "Off", folder: "Shared folder", webdav: "WebDAV" };

export function DevicesPage({ filters, setFilters, go, notify, status }: PageProps) {
  const { query, live } = toQuery({ ...filters, device: "" }, { limit: 1 });
  const { report } = useReport(query, live);
  const mesh = useEventValue<MeshStatus>(() => Monitor.mesh(), (fn) => events.mesh.on(fn));
  const sync = useEventValue<SyncStatus>(() => Sync.status(), (fn) => events.sync.on(fn));
  const selfLive = useEventValue(() => Monitor.live(), (fn) => events.live.on(fn));
  const [busy, setBusy] = useState(false);
  const devices = report?.devices ?? [];
  const total = devices.reduce((s, d) => s + d.up + d.down, 0);
  const max = devices.reduce((m, d) => Math.max(m, d.up + d.down), 0) || 1;
  const peerOf = (id: string) => mesh?.peers.find((p) => p.deviceId === id);

  const syncNow = async () => {
    setBusy(true);
    try {
      const s = await Sync.now();
      notify(t("Synced: {up} files uploaded, {down} downloaded", { up: s.uploaded, down: s.downloaded }));
    } catch (err) {
      notify(errorText(err), "error");
    } finally {
      setBusy(false);
    }
  };

  const forget = async (id: string) => {
    try {
      if (await Traffic.forgetDevice(id)) notify(t("Deleted this computer's copy of the device's data"));
    } catch (err) {
      notify(errorText(err), "error");
    }
  };

  return (
    <>
      <FilterBar filters={filters} setFilters={setFilters} hide={["device"]} />
      <div class="stack">
        <TailscaleCard mesh={mesh} go={go} />

        <div class="device-grid">
          {devices.map((d) => {
            const sum = d.up + d.down;
            const peer = peerOf(d.device.id);
            const online = d.self || peer?.state === "app";
            const speed = d.self ? (selfLive && status?.connected ? selfLive : null) : peer?.live.connected ? peer.live : null;
            const current = d.device.subs?.find((s) => s.current);
            return (
              <Card
                key={d.device.id}
                title={
                  <>
                    {d.device.name}
                    {d.self && <span class="badge">{t("This computer")}</span>}
                  </>
                }
                extra={
                  !d.self && (
                    <button type="button" class="ghost small" onClick={() => forget(d.device.id)}>
                      {t("Delete")}
                    </button>
                  )
                }
              >
                <div class="device-online">
                  <span class={`status-dot ${online ? "good" : "idle"}`} aria-hidden="true" />
                  {online
                    ? speed
                      ? `${t("Online")} · ↓ ${rate(speed.downRate)} ↑ ${rate(speed.upRate)}`
                      : `${t("Online")} · ${t("Not connected to Clash")}`
                    : `${t("Offline")} · ${t("Data updated {ago}", { ago: ago(d.device.updated) })}`}
                </div>
                <div class="device-total">{bytes(sum)}</div>
                <div class="bar-track wide">
                  {d.down > 0 && <span class="bar-fill s-down" style={{ width: `${Math.max(0.4, (d.down / max) * 100)}%` }} />}
                  {d.up > 0 && <span class="bar-fill s-up" style={{ width: `${Math.max(0.4, (d.up / max) * 100)}%` }} />}
                </div>
                <dl class="kv">
                  <dt>{t("Download / upload")}</dt>
                  <dd>
                    {bytes(d.down)} / {bytes(d.up)}
                  </dd>
                  <dt>{t("Share of all devices")}</dt>
                  <dd>{percent(sum, total)}</dd>
                  <dt>{t("Connections")}</dt>
                  <dd>{count(d.conns)}</dd>
                  <dt>{t("Current subscription")}</dt>
                  <dd>{current ? subLabel(current.name) : "—"}</dd>
                  <dt>{t("System")}</dt>
                  <dd>
                    {osName(d.device.os)} {d.device.arch}
                  </dd>
                  {peer && (
                    <>
                      <dt>Tailscale</dt>
                      <dd>
                        {peer.host} · {peer.ip}
                      </dd>
                    </>
                  )}
                </dl>
                <button type="button" class="link" onClick={() => (setFilters({ device: d.device.id }), go("overview"))}>
                  {t("Show this device only")} ›
                </button>
              </Card>
            );
          })}
        </div>

        {sync && sync.kind !== "off" && (
          <Card
            title={t("Other ways to sync")}
            extra={
              <button type="button" class="primary" disabled={busy || sync.running} onClick={syncNow}>
                {busy || sync.running ? t("Syncing…") : t("Sync now")}
              </button>
            }
          >
            <div class="sync-grid">
              <div>
                <span class="muted">{t("Method")}</span>
                <strong>{t(KIND[sync.kind] ?? sync.kind)}</strong>
              </div>
              <div>
                <span class="muted">{t("Last sync")}</span>
                <strong>{sync.lastSync ? ago(sync.lastSync) : t("Not synced yet")}</strong>
              </div>
              <div>
                <span class="muted">{t("Other devices there")}</span>
                <strong>{sync.devices}</strong>
              </div>
              <div>
                <span class="muted">{t("Last transfer")}</span>
                <strong>{tn("↑ {up} · ↓ {n} files", sync.downloaded, { up: sync.uploaded })}</strong>
              </div>
              {sync.lastError && <div class="banner error wide">{t("Sync failed: {error}", { error: sync.lastError })}</div>}
            </div>
          </Card>
        )}
      </div>
    </>
  );
}

/** The states of a peer, their labels in English: t() shows them. */
const STATE: Record<string, { label: string; dot: string }> = {
  app: { label: "Runs Clash Traffic", dot: "good" },
  noapp: { label: "Does not run this app", dot: "idle" },
  offline: { label: "Offline", dot: "idle" },
};

function TailscaleCard({ mesh, go }: { mesh: MeshStatus | null; go: PageProps["go"] }) {
  const [scanning, setScanning] = useState(false);
  const scan = async () => {
    setScanning(true);
    await Monitor.scanPeers().catch(() => {});
    setTimeout(() => setScanning(false), 1500);
  };
  if (!mesh) return null;
  const found = mesh.peers.filter((p) => p.state === "app");
  return (
    <Card
      title={t("Tailscale sync")}
      extra={
        <div class="card-tools">
          {mesh.lastScan > 0 && <span class="muted small">{t("Scanned {ago}", { ago: ago(mesh.lastScan) })}</span>}
          <button type="button" class="ghost" disabled={!mesh.enabled || scanning || mesh.scanning} onClick={scan}>
            {scanning || mesh.scanning ? t("Scanning…") : t("Scan now")}
          </button>
        </div>
      }
    >
      {!mesh.enabled ? (
        <p class="muted">
          {t("Off.")}{" "}
          <button type="button" class="link" onClick={() => go("settings")}>
            {t("Turn it on in Settings")}
          </button>
        </p>
      ) : !mesh.available ? (
        <div class="banner warning">
          <strong>{mesh.error || t("Looking for Tailscale…")}</strong>
          <div class="small">
            {t("Once Tailscale is installed and signed in, the computers of the tailnet that run Clash Traffic find each other and merge their numbers, with no setup.")}
          </div>
        </div>
      ) : (
        <>
          <div class="ts-head">
            <span class={`status-dot ${mesh.serving ? "good" : "warning"}`} aria-hidden="true" />
            <span>
              {t("This computer")} <strong>{mesh.selfHost}</strong> · {mesh.selfIP}:{mesh.port}
              {mesh.tailnet && <span class="muted"> · {mesh.tailnet}</span>}
            </span>
            <span class="muted"> · {tn("{n} devices running Clash Traffic found, synced every minute", found.length)}</span>
          </div>
          {mesh.serveError && <div class="banner error">{t("Other devices cannot read from this computer: {error}", { error: mesh.serveError })}</div>}
          <div class="table-wrap ts-table">
            <table class="table">
              <thead>
                <tr>
                  <th>{t("Device")}</th>
                  <th>{t("Address")}</th>
                  <th>{t("Status")}</th>
                  <th class="num">{t("Live speed")}</th>
                  <th class="num">{t("Last sync")}</th>
                </tr>
              </thead>
              <tbody>
                {mesh.peers.map((p) => (
                  <PeerRow key={p.ip} p={p} />
                ))}
              </tbody>
            </table>
          </div>
          <p class="hint footnote">
            {t("Only your own devices of the tailnet, not those shared into it, can read this computer's numbers; the access log is not shared.")}
          </p>
        </>
      )}
    </Card>
  );
}

function PeerRow({ p }: { p: PeerStatus }) {
  const st = STATE[p.state] ?? { label: p.state, dot: "idle" };
  return (
    <tr>
      <td class="name-cell">
        <div class="name">{p.deviceName || p.host}</div>
        <div class="detail">
          {p.deviceName && p.deviceName !== p.host ? `${p.host} · ` : ""}
          {p.os}
        </div>
      </td>
      <td class="muted">{p.ip}</td>
      <td title={p.error}>
        <span class={`status-dot ${st.dot}`} aria-hidden="true" /> {t(st.label)}
        {p.state === "app" && p.error && <div class="detail error-text">{p.error}</div>}
      </td>
      <td class="num">{p.state === "app" && p.live.connected ? `↓ ${rate(p.live.downRate)} ↑ ${rate(p.live.upRate)}` : "—"}</td>
      <td class="num">{p.lastPull ? ago(p.lastPull) : "—"}</td>
    </tr>
  );
}
