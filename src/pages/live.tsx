import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { Card, ROUTE_LABEL, RouteSwitch, type PageProps } from "../app";
import { Legend, RateChart } from "../components/charts";
import { errorText } from "../lib/hooks";
import { bytes, duration, rate } from "../lib/format";
import { events, Monitor, type Live } from "../mygo";
import { appendRate, newerLive, type RateState } from "../lib/live";

type Sort = "speed" | "total" | "start";

export function LivePage({ status, notify, filters, setFilters }: PageProps) {
  const [live, setLive] = useState<Live | null>(null);
  const [rateState, setRateState] = useState<RateState>({ rates: [], run: "", seq: 0 });
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<Sort>("speed");
  const [byApp, setByApp] = useState(false);

  // The live event lists the busiest connections only: searching, sorting
  // otherwise or filtering needs every one, which Monitor.live returns.
  const needAll = search.trim() !== "" || sort !== "speed" || filters.route !== "" || byApp;
  const needAllRef = useRef(needAll);
  needAllRef.current = needAll;
  const fetching = useRef(false);
  const fetchAll = () => {
    if (fetching.current) return;
    fetching.current = true;
    Monitor.live()
      .then((l) => setLive((cur) => newerLive(cur, l)))
      .catch(() => {})
      .finally(() => (fetching.current = false));
  };
  useEffect(() => {
    if (needAll) fetchAll();
  }, [needAll]);

  useEffect(() => {
    Monitor.rates()
      .then((rates) => setRateState((cur) => (cur.rates.length ? cur : { ...cur, rates })))
      .catch(() => {});
    Monitor.live().then((l) => setLive((cur) => newerLive(cur, l))).catch(() => {});
    return events.live.on((l) => {
      if (needAllRef.current) fetchAll();
      else setLive((cur) => newerLive(cur, l));
      setRateState((cur) => appendRate(cur, l));
    });
  }, []);

  const conns = useMemo(() => {
    const q = search.trim().toLowerCase();
    let list = live?.conns ?? [];
    if (filters.route) list = list.filter((c) => c.route === filters.route);
    if (q) list = list.filter((c) => [c.app, c.process, c.host, c.dest, c.chains, c.rule].some((s) => s.toLowerCase().includes(q)));
    const sorted = [...list];
    if (sort === "total") sorted.sort((a, b) => b.up + b.down - (a.up + a.down));
    if (sort === "start") sorted.sort((a, b) => b.start - a.start);
    return sorted;
  }, [live, search, sort, filters.route]);

  const groups = useMemo(() => {
    const m = new Map<string, { app: string; n: number; up: number; down: number; upRate: number; downRate: number }>();
    for (const c of conns) {
      const g = m.get(c.app) ?? { app: c.app, n: 0, up: 0, down: 0, upRate: 0, downRate: 0 };
      g.n++;
      g.up += c.up;
      g.down += c.down;
      g.upRate += c.upRate;
      g.downRate += c.downRate;
      m.set(c.app, g);
    }
    return [...m.values()].sort((a, b) => b.downRate + b.upRate - (a.downRate + a.upRate) || b.down + b.up - (a.down + a.up));
  }, [conns]);

  const close = async (id: string) => {
    try {
      await Monitor.closeConnection(id);
    } catch (err) {
      notify(errorText(err), "error");
    }
  };

  const now = live?.t ?? Date.now();
  return (
    <div class="stack">
      {!status?.connected && <div class="banner error">未连接到 Clash：{status?.error || "正在查找…"}</div>}
      <div class="hero-row">
        <div class="stat">
          <div class="stat-label">
            <span class="key key-line s-down" aria-hidden="true" />
            下载速度
          </div>
          <div class="stat-value">{rate(live?.downRate ?? 0)}</div>
        </div>
        <div class="stat">
          <div class="stat-label">
            <span class="key key-line s-up" aria-hidden="true" />
            上传速度
          </div>
          <div class="stat-value">{rate(live?.upRate ?? 0)}</div>
        </div>
        <div class="stat">
          <div class="stat-label">活动连接</div>
          <div class="stat-value">{live?.total ?? 0}</div>
        </div>
        <div class="stat">
          <div class="stat-label">内核启动以来</div>
          <div class="stat-value small">
            下载 {bytes(live?.coreDown ?? 0)}
            <br />
            上传 {bytes(live?.coreUp ?? 0)}
          </div>
        </div>
      </div>
      <Card title="最近 5 分钟" extra={<Legend kind="line" />}>
        <RateChart points={rateState.rates} />
      </Card>
      <Card
        title={needAll || !live ? `活动连接（${conns.length}）` : `活动连接（${live.total}）`}
        extra={
          <div class="card-tools">
            <RouteSwitch value={filters.route} onChange={(route) => setFilters({ route })} />
            <label class="switch">
              <input type="checkbox" checked={byApp} onChange={(e) => setByApp(e.currentTarget.checked)} />
              按应用分组
            </label>
            <select value={sort} onChange={(e) => setSort(e.currentTarget.value as Sort)} aria-label="排序">
              <option value="speed">按速度</option>
              <option value="total">按流量</option>
              <option value="start">按开始时间</option>
            </select>
            <input type="search" class="search" placeholder="搜索应用、地址、节点、规则" value={search} onInput={(e) => setSearch(e.currentTarget.value)} />
          </div>
        }
      >
        <div class="table-wrap tall">
          {byApp ? (
            <table class="table">
              <thead>
                <tr>
                  <th>应用</th>
                  <th class="num">连接</th>
                  <th class="num">下载速度</th>
                  <th class="num">上传速度</th>
                  <th class="num">已下载</th>
                  <th class="num">已上传</th>
                </tr>
              </thead>
              <tbody>
                {groups.map((g) => (
                  <tr key={g.app}>
                    <td class="name-cell">
                      <div class="name">{g.app}</div>
                    </td>
                    <td class="num">{g.n}</td>
                    <td class="num strong">{rate(g.downRate)}</td>
                    <td class="num">{rate(g.upRate)}</td>
                    <td class="num">{bytes(g.down)}</td>
                    <td class="num">{bytes(g.up)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <table class="table">
              <thead>
                <tr>
                  <th class="col-app">应用</th>
                  <th class="col-host">地址</th>
                  <th class="col-chain">节点 / 规则</th>
                  <th class="num">下载速度</th>
                  <th class="num">上传速度</th>
                  <th class="num">已下载</th>
                  <th class="num">已上传</th>
                  <th class="num">时长</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {conns.map((c) => (
                  <tr key={c.id}>
                    <td class="name-cell col-app" title={c.process}>
                      <div class="name">{c.app}</div>
                      {c.process && c.process !== c.app && <div class="detail">{c.process}</div>}
                    </td>
                    <td class="name-cell col-host" title={c.host}>
                      <div class="name">{c.host}</div>
                      <div class="detail">
                        {c.network.toUpperCase()}
                        {c.dest && ` · ${c.dest}`}
                      </div>
                    </td>
                    <td class="name-cell col-chain" title={c.chains}>
                      <div class="name">
                        <span class={`route-tag ${c.route}`}>{c.route === "proxy" && c.sub ? c.sub : (ROUTE_LABEL[c.route] ?? c.route)}</span>
                        {c.chains || c.chain || "—"}
                      </div>
                      <div class="detail">{c.rule}</div>
                    </td>
                    <td class="num strong">{rate(c.downRate)}</td>
                    <td class="num">{rate(c.upRate)}</td>
                    <td class="num">{bytes(c.down)}</td>
                    <td class="num">{bytes(c.up)}</td>
                    <td class="num">{duration(now - c.start)}</td>
                    <td>
                      <button type="button" class="icon-btn" title="断开这个连接" aria-label="断开连接" onClick={() => close(c.id)}>
                        ×
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {live && !conns.length && <div class="empty">{search ? "没有匹配的连接" : "没有活动连接"}</div>}
          {live && !needAll && live.total > conns.length && (
            <div class="empty small">只列出速度最快的 {conns.length} 个；搜索、切换排序或筛选时会列出全部 {live.total} 个</div>
          )}
        </div>
      </Card>
    </div>
  );
}
