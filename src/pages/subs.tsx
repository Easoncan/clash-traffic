import { Card, FilterBar, subLabel, toQuery, type PageProps } from "../app";
import { useReport } from "../lib/hooks";
import { bytes, percent } from "../lib/format";
import { rangeLabel } from "../lib/range";
import type { Report, SubInfo } from "../mygo";

interface SubView {
  name: string;
  info: SubInfo | null; // the provider's figures, the freshest any device has
  inUse: string[]; // the devices using it now
  up: number;
  down: number;
  devices: { id: string; name: string; up: number; down: number }[];
}

/** The subscriptions of every device, with the provider's usage and ours. */
function subViews(report: Report): SubView[] {
  const names = new Map<string, string>(report.devices.map((d) => [d.device.id, d.device.name + (d.self ? "（本机）" : "")]));
  const views = new Map<string, SubView>();
  const view = (name: string) => {
    let v = views.get(name);
    if (!v) {
      v = { name, info: null, inUse: [], up: 0, down: 0, devices: [] };
      views.set(name, v);
    }
    return v;
  };
  for (const d of report.devices) {
    for (const s of d.device.subs ?? []) {
      const v = view(s.name);
      if (!v.info || s.updated > v.info.updated) v.info = s;
      if (s.current) v.inUse.push(names.get(d.device.id) ?? d.device.id);
    }
  }
  for (const it of report.subs) {
    const v = view(it.name);
    v.up = it.up;
    v.down = it.down;
  }
  for (const sd of report.subDevices) {
    view(sd.sub).devices.push({ id: sd.device, name: names.get(sd.device) ?? sd.device, up: sd.up, down: sd.down });
  }
  return [...views.values()].sort((a, b) => {
    const rank = (v: SubView) => (v.name === "" ? 2 : v.inUse.length ? 0 : 1);
    return rank(a) - rank(b) || b.up + b.down - (a.up + a.down) || a.name.localeCompare(b.name);
  });
}

export function SubsPage({ filters, setFilters, go }: PageProps) {
  const { query, live } = toQuery({ ...filters, sub: null }, { limit: 1 });
  const { report, loading } = useReport(query, live);
  const views = report ? subViews(report) : [];
  const counted = views.reduce((s, v) => s + v.up + v.down, 0);
  const show = (sub: string) => {
    setFilters({ sub });
    go("overview");
  };

  return (
    <>
      <FilterBar filters={filters} setFilters={setFilters} hide={["sub"]} />
      <div class={`stack${loading && report ? " refreshing" : ""}`}>
        <p class="muted small intro">
          走代理的流量记到 Clash Verge 当时启用的订阅（节点来自 proxy-provider 时记到该 provider），直连流量不消耗订阅。
          「机场用量」来自订阅返回的信息，统计的是这个订阅在所有设备上的总用量；「本应用统计」是 Clash Traffic 在{rangeLabel(filters.range)}记录到的流量。
        </p>
        {report && !views.length && <div class="placeholder">还没有订阅信息：确认 Clash Verge 已导入订阅。</div>}
        <div class="sub-grid">
          {views.map((v) => (
            <SubCard key={v.name} v={v} total={counted} onShow={() => show(v.name)} />
          ))}
        </div>
      </div>
    </>
  );
}

function SubCard({ v, total, onShow }: { v: SubView; total: number; onShow: () => void }) {
  const sum = v.up + v.down;
  const max = v.devices.reduce((m, d) => Math.max(m, d.up + d.down), 0) || 1;
  return (
    <Card
      class="sub-card"
      title={
        <>
          {subLabel(v.name)}
          {v.inUse.length > 0 && <span class="badge live">使用中</span>}
        </>
      }
      extra={
        <button type="button" class="ghost small" onClick={onShow}>
          只看它的流量 ›
        </button>
      }
    >
      {v.inUse.length > 0 && <div class="muted small">正在使用：{v.inUse.join("、")}</div>}
      {v.name !== "" && <Quota info={v.info} />}
      {v.name === "" && <p class="muted small">直连和被拦截的流量，不经过任何订阅的节点，不计入机场流量。</p>}

      <div class="sub-counted">
        <div class="sub-section-title">本应用统计</div>
        <div class="device-total">{bytes(sum)}</div>
        <div class="muted small">
          下载 {bytes(v.down)} · 上传 {bytes(v.up)} · 占全部 {percent(sum, total)}
        </div>
        {v.devices.length > 0 && (
          <ul class="sub-devices">
            {v.devices.map((d) => (
              <li key={d.id}>
                <span class="bar-name">{d.name}</span>
                <span class="bar-value">{bytes(d.up + d.down)}</span>
                <span class="bar-track">
                  {d.down > 0 && <span class="bar-fill s-down" style={{ width: `${Math.max(0.4, (d.down / max) * 100)}%` }} />}
                  {d.up > 0 && <span class="bar-fill s-up" style={{ width: `${Math.max(0.4, (d.up / max) * 100)}%` }} />}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Card>
  );
}

/** The plan's usage, as the provider reports it. */
function Quota({ info }: { info: SubInfo | null }) {
  if (!info || (!info.total && !info.upload && !info.download && !info.expire)) {
    return <p class="muted small">订阅没有提供用量信息。</p>;
  }
  const used = info.upload + info.download;
  const ratio = info.total > 0 ? Math.min(1, used / info.total) : 0;
  const level = ratio >= 0.95 ? "critical" : ratio >= 0.8 ? "warning" : "normal";
  const days = info.expire ? Math.ceil((info.expire * 1000 - Date.now()) / 86_400_000) : null;
  return (
    <div class="quota">
      <div class="sub-section-title">机场用量</div>
      {info.total > 0 ? (
        <>
          <div class="quota-line">
            <strong>{bytes(used)}</strong>
            <span class="muted"> / {bytes(info.total)}</span>
            <span class={`quota-pct ${level}`}>
              {level !== "normal" && (level === "critical" ? "⚠ 即将用完 · " : "⚠ 用量较高 · ")}
              {percent(used, info.total)}
            </span>
          </div>
          <div class="meter" role="meter" aria-valuemin={0} aria-valuemax={info.total} aria-valuenow={used} aria-label="订阅已用流量">
            <span class={`meter-fill ${level}`} style={{ width: `${Math.max(ratio * 100, used > 0 ? 0.8 : 0)}%` }} />
          </div>
          <div class="muted small">剩余 {bytes(Math.max(0, info.total - used))}</div>
        </>
      ) : (
        <div class="quota-line">
          <strong>{bytes(used)}</strong>
          <span class="muted"> 已用（订阅未提供总量）</span>
        </div>
      )}
      <div class="muted small">
        {days !== null && info.expire ? (days < 0 ? "已过期" : `${new Date(info.expire * 1000).toLocaleDateString("zh-CN")} 到期，还有 ${days} 天`) : "无到期时间"}
        {info.updated ? ` · 订阅更新于 ${new Date(info.updated * 1000).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}` : ""}
      </div>
    </div>
  );
}
