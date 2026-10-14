import { useEffect, useRef, useState } from "preact/hooks";
import { Card, FilterBar, ROUTE_LABEL, subLabel, type PageProps } from "../app";
import { errorText } from "../lib/hooks";
import { bytes, count, dateTime, duration } from "../lib/format";
import { resolve } from "../lib/range";
import { events, Traffic, type DeviceInfo, type Session, type SessionPage } from "../mygo";

const PAGE = 200;

/** This computer's connections, one by one: when, which app, where to. */
export function RecordsPage({ filters, setFilters }: PageProps) {
  const [search, setSearch] = useState("");
  const [debounced, setDebounced] = useState("");
  const [limit, setLimit] = useState(PAGE);
  const [page, setPage] = useState<SessionPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [self, setSelf] = useState<DeviceInfo | null>(null);
  const seq = useRef(0);

  useEffect(() => {
    const t = setTimeout(() => setDebounced(search), 250);
    return () => clearTimeout(t);
  }, [search]);
  useEffect(() => {
    Traffic.devices()
      .then((d) => setSelf(d[0] ?? null))
      .catch(() => {});
  }, []);

  const { from, to, live } = resolve(filters.range);
  const scope = JSON.stringify([from, to, filters.app, filters.host, filters.chain, filters.route, filters.sub, filters.groupDomain, debounced]);
  useEffect(() => setLimit(PAGE), [scope]);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      const n = ++seq.current;
      setLoading(true);
      try {
        const p = await Traffic.sessions({
          from,
          to,
          app: filters.app,
          host: filters.host,
          chain: filters.chain,
          route: filters.route,
          sub: filters.sub ?? "",
          noSub: filters.sub === "",
          groupDomain: filters.groupDomain,
          search: debounced,
          offset: 0,
          limit,
        });
        if (alive && n === seq.current) {
          setPage(p);
          setError("");
        }
      } catch (err) {
        if (alive && n === seq.current) setError(errorText(err));
      } finally {
        if (alive && n === seq.current) setLoading(false);
      }
    };
    void load();
    const timer = live ? setInterval(load, 10_000) : undefined;
    const off = events.data.on(() => void load());
    return () => {
      alive = false;
      if (timer) clearInterval(timer);
      off();
    };
  }, [scope, limit, live]);

  const otherDevice = filters.device && self && filters.device !== self.id;
  const items = page?.items ?? [];

  return (
    <>
      <FilterBar filters={filters} setFilters={setFilters} hide={["device"]} />
      {otherDevice && <div class="banner warning">访问记录只保存在各自的电脑上，这里显示的是本机（{self?.name}）的记录。</div>}
      {error && <div class="banner error">读取访问记录失败：{error}</div>}
      <Card
        class={loading && page ? "refreshing" : ""}
        title={page ? `${count(page.total)} 个连接 · 下载 ${bytes(page.down)} · 上传 ${bytes(page.up)}` : "访问记录"}
        extra={<input type="search" class="search wide" placeholder="搜索应用、地址、IP、节点、规则" value={search} onInput={(e) => setSearch(e.currentTarget.value)} />}
      >
        <div class="table-wrap records">
          <table class="table">
            <thead>
              <tr>
                <th class="col-time">开始时间</th>
                <th class="num col-dur">时长</th>
                <th class="col-app">应用</th>
                <th class="col-host">地址</th>
                <th class="col-chain">节点 / 规则</th>
                <th class="num">下载</th>
                <th class="num">上传</th>
              </tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <Row key={s.id} s={s} onApp={(app) => setFilters({ app })} onHost={(host) => setFilters({ host, groupDomain: false })} />
              ))}
            </tbody>
          </table>
          {page && !items.length && <div class="empty">{debounced || filters.app || filters.host || filters.chain ? "没有匹配的连接" : "这段时间没有访问记录"}</div>}
          {page && page.total > items.length && (
            <div class="more">
              <button type="button" class="ghost" onClick={() => setLimit(limit + PAGE)}>
                再显示 {Math.min(PAGE, page.total - items.length)} 条（共 {count(page.total)} 条）
              </button>
            </div>
          )}
        </div>
        <p class="hint footnote">
          记录本机每个连接的开始时间、应用、目标地址、节点和流量，只保存在本机，不参与多设备同步。
          {page?.since ? ` 最早的记录在 ${new Date(page.since).toLocaleDateString("zh-CN")}。` : ""}
          在两次采样（1 秒）之间开始又结束的极短连接不会出现在这里；连接的字节数是最后一次看到它时的数字。
        </p>
      </Card>
    </>
  );
}

function Row({ s, onApp, onHost }: { s: Session; onApp: (app: string) => void; onHost: (host: string) => void }) {
  const detail = [
    s.route === "proxy" && `订阅：${subLabel(s.sub)}`,
    s.path && `路径：${s.path}`,
    s.process && s.process !== s.app && `进程：${s.process}`,
    s.source && `来源：${s.source}`,
    s.inbound && `入站：${s.inbound}`,
    `结束：${s.open ? "进行中" : s.cut ? `${dateTime(s.end)} 之后（应用未运行）` : dateTime(s.end)}`,
  ]
    .filter(Boolean)
    .join("\n");
  return (
    <tr title={detail}>
      <td class="col-time">
        <div>{dateTime(s.start)}</div>
        {s.open && <span class="badge live">进行中</span>}
        {s.cut && (
          <span class="badge" title="应用退出时这个连接还在进行，再次打开时它已结束；结束时间和最后的流量未知">
            应用退出时仍在进行
          </span>
        )}
      </td>
      <td class="num col-dur">
        {s.cut ? "≥ " : ""}
        {duration(s.end - s.start)}
      </td>
      <td class="name-cell col-app">
        <button type="button" class="cell-link name" onClick={() => onApp(s.app)}>
          {s.app}
        </button>
        {s.process && s.process !== s.app && <div class="detail">{s.process}</div>}
      </td>
      <td class="name-cell col-host">
        <button type="button" class="cell-link name" onClick={() => onHost(s.host)}>
          {s.host}
        </button>
        <div class="detail">
          {s.network.toUpperCase()}
          {s.dest && ` · ${s.dest}`}
        </div>
      </td>
      <td class="name-cell col-chain">
        <div class="name">
          <span class={`route-tag ${s.route}`}>{s.route === "proxy" && s.sub ? s.sub : (ROUTE_LABEL[s.route] ?? s.route)}</span>
          {s.chains || s.chain}
        </div>
        <div class="detail">{s.rule}</div>
      </td>
      <td class="num strong">{bytes(s.down)}</td>
      <td class="num">{bytes(s.up)}</td>
    </tr>
  );
}
