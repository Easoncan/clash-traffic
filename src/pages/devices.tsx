import { useState } from "preact/hooks";
import { Card, FilterBar, toQuery, type PageProps } from "../app";
import { errorText, useEventValue, useReport } from "../lib/hooks";
import { ago, bytes, count, osName, percent, rate } from "../lib/format";
import { events, Monitor, Sync, Traffic, type MeshStatus, type PeerStatus, type SyncStatus } from "../mygo";

const KIND: Record<string, string> = { off: "未开启", folder: "共享文件夹", webdav: "WebDAV" };

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
      notify(`同步完成：上传 ${s.uploaded} 个文件，下载 ${s.downloaded} 个文件`);
    } catch (err) {
      notify(errorText(err), "error");
    } finally {
      setBusy(false);
    }
  };

  const forget = async (id: string) => {
    try {
      if (await Traffic.forgetDevice(id)) notify("已删除该设备在本机的数据");
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
                    {d.self && <span class="badge">本机</span>}
                  </>
                }
                extra={
                  !d.self && (
                    <button type="button" class="ghost small" onClick={() => forget(d.device.id)}>
                      删除
                    </button>
                  )
                }
              >
                <div class="device-online">
                  <span class={`status-dot ${online ? "good" : "idle"}`} aria-hidden="true" />
                  {online ? (speed ? `在线 · ↓ ${rate(speed.downRate)} ↑ ${rate(speed.upRate)}` : "在线 · 未连接 Clash") : `离线 · 数据更新于 ${ago(d.device.updated)}`}
                </div>
                <div class="device-total">{bytes(sum)}</div>
                <div class="bar-track wide">
                  {d.down > 0 && <span class="bar-fill s-down" style={{ width: `${Math.max(0.4, (d.down / max) * 100)}%` }} />}
                  {d.up > 0 && <span class="bar-fill s-up" style={{ width: `${Math.max(0.4, (d.up / max) * 100)}%` }} />}
                </div>
                <dl class="kv">
                  <dt>下载 / 上传</dt>
                  <dd>
                    {bytes(d.down)} / {bytes(d.up)}
                  </dd>
                  <dt>占全部设备</dt>
                  <dd>{percent(sum, total)}</dd>
                  <dt>连接数</dt>
                  <dd>{count(d.conns)}</dd>
                  <dt>当前订阅</dt>
                  <dd>{current ? current.name : "—"}</dd>
                  <dt>系统</dt>
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
                  只看这台设备 ›
                </button>
              </Card>
            );
          })}
        </div>

        {sync && sync.kind !== "off" && (
          <Card
            title="其他同步方式"
            extra={
              <button type="button" class="primary" disabled={busy || sync.running} onClick={syncNow}>
                {busy || sync.running ? "同步中…" : "立即同步"}
              </button>
            }
          >
            <div class="sync-grid">
              <div>
                <span class="muted">方式</span>
                <strong>{KIND[sync.kind]}</strong>
              </div>
              <div>
                <span class="muted">上次同步</span>
                <strong>{sync.lastSync ? ago(sync.lastSync) : "尚未同步"}</strong>
              </div>
              <div>
                <span class="muted">远端其他设备</span>
                <strong>{sync.devices}</strong>
              </div>
              <div>
                <span class="muted">上次传输</span>
                <strong>
                  ↑ {sync.uploaded} · ↓ {sync.downloaded} 个文件
                </strong>
              </div>
              {sync.lastError && <div class="banner error wide">同步出错：{sync.lastError}</div>}
            </div>
          </Card>
        )}
      </div>
    </>
  );
}

const STATE: Record<string, { label: string; dot: string }> = {
  app: { label: "运行 Clash Traffic", dot: "good" },
  noapp: { label: "未运行本应用", dot: "idle" },
  offline: { label: "离线", dot: "idle" },
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
      title="Tailscale 自动同步"
      extra={
        <div class="card-tools">
          {mesh.lastScan > 0 && <span class="muted small">扫描于 {ago(mesh.lastScan)}</span>}
          <button type="button" class="ghost" disabled={!mesh.enabled || scanning || mesh.scanning} onClick={scan}>
            {scanning || mesh.scanning ? "扫描中…" : "立即扫描"}
          </button>
        </div>
      }
    >
      {!mesh.enabled ? (
        <p class="muted">
          已关闭。
          <button type="button" class="link" onClick={() => go("settings")}>
            在设置中开启
          </button>
        </p>
      ) : !mesh.available ? (
        <div class="banner warning">
          <strong>{mesh.error || "正在查找 Tailscale…"}</strong>
          <div class="small">安装并登录 Tailscale 后，同一 tailnet 中运行 Clash Traffic 的电脑会自动互相发现、合并统计，无需任何配置。</div>
        </div>
      ) : (
        <>
          <div class="ts-head">
            <span class={`status-dot ${mesh.serving ? "good" : "warning"}`} aria-hidden="true" />
            <span>
              本机 <strong>{mesh.selfHost}</strong> · {mesh.selfIP}:{mesh.port}
              {mesh.tailnet && <span class="muted"> · {mesh.tailnet}</span>}
            </span>
            <span class="muted">
              · 发现 {found.length} 台运行 Clash Traffic 的设备，每分钟同步一次
            </span>
          </div>
          {mesh.serveError && <div class="banner error">其他设备无法从本机读取数据：{mesh.serveError}</div>}
          <div class="table-wrap ts-table">
            <table class="table">
              <thead>
                <tr>
                  <th>设备</th>
                  <th>地址</th>
                  <th>状态</th>
                  <th class="num">实时速度</th>
                  <th class="num">最近同步</th>
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
            只有同一 tailnet 中你自己的设备（不含他人共享进来的设备）能读取本机的统计数据；访问记录不会共享。
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
        <span class={`status-dot ${st.dot}`} aria-hidden="true" /> {st.label}
        {p.state === "app" && p.error && <div class="detail error-text">{p.error}</div>}
      </td>
      <td class="num">{p.state === "app" && p.live.connected ? `↓ ${rate(p.live.downRate)} ↑ ${rate(p.live.upRate)}` : "—"}</td>
      <td class="num">{p.lastPull ? ago(p.lastPull) : "—"}</td>
    </tr>
  );
}
