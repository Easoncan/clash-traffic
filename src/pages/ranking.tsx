import { useEffect, useState } from "preact/hooks";
import { Card, FilterBar, toQuery, type PageProps } from "../app";
import { BarList, ColumnChart, Legend } from "../components/charts";
import { chainLabel } from "./overview";
import { errorText, useReport } from "../lib/hooks";
import { bytes, count, percent } from "../lib/format";
import { Traffic } from "../mygo";

/** The table of applications, or of hosts, with what the picked one did. */
export function Ranking({ kind, ...props }: PageProps & { kind: "apps" | "hosts" }) {
  const { filters, setFilters, notify } = props;
  const [search, setSearch] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebounced(search), 250);
    return () => clearTimeout(t);
  }, [search]);
  const { query, live } = toQuery(filters, { limit: 500, search: debounced });
  const { report, loading, error } = useReport(query, live);

  const isApps = kind === "apps";
  const items = (isApps ? report?.apps : report?.hosts) ?? [];
  const itemCount = (isApps ? report?.appCount : report?.hostCount) ?? 0;
  const selected = isApps ? filters.app : filters.host;
  const select = (name: string) => setFilters(isApps ? { app: selected === name ? "" : name } : { host: selected === name ? "" : name });
  // The page's own dimension is listed whole: its share is of the whole too.
  const grand = (isApps ? report?.appsTotal : report?.hostsTotal) ?? 0;
  const max = items.reduce((m, i) => Math.max(m, i.up + i.down), 0) || 1;
  const noun = isApps ? "应用" : filters.groupDomain ? "域名" : "地址";

  const exportCSV = async () => {
    try {
      const path = await Traffic.export(query);
      if (path) notify(`已导出到 ${path}`);
    } catch (err) {
      notify(errorText(err), "error");
    }
  };

  return (
    <>
      <FilterBar filters={filters} setFilters={setFilters} hide={isApps ? ["app"] : ["host"]}>
        {!isApps && (
          <label class="switch">
            <input type="checkbox" checked={filters.groupDomain} onChange={(e) => setFilters({ groupDomain: e.currentTarget.checked, host: "" })} />
            按主域名合并
          </label>
        )}
        <button type="button" class="ghost" onClick={exportCSV}>
          导出 CSV
        </button>
      </FilterBar>
      {error && <div class="banner error">读取数据失败：{error}</div>}
      <div class={`split${loading && report ? " refreshing" : ""}`}>
        <Card
          class="fill"
          title={`${noun}（${itemCount}）`}
          extra={<input type="search" class="search" placeholder={`搜索${noun}`} value={search} onInput={(e) => setSearch(e.currentTarget.value)} />}
        >
          <div class="table-wrap">
            <table class="table selectable">
              <thead>
                <tr>
                  <th class="rank">#</th>
                  <th>{noun}</th>
                  <th class="num">下载</th>
                  <th class="num">上传</th>
                  <th class="num">合计</th>
                  <th class="bar-col" aria-label="占比条" />
                  <th class="num">占比</th>
                  <th class="num">连接</th>
                </tr>
              </thead>
              <tbody>
                {items.map((it, i) => {
                  const sum = it.up + it.down;
                  return (
                    <tr key={it.name} class={selected === it.name ? "selected" : ""} onClick={() => select(it.name)} tabIndex={0} onKeyDown={(e) => e.key === "Enter" && select(it.name)}>
                      <td class="rank">{i + 1}</td>
                      <td class="name-cell" title={it.detail || it.name}>
                        <div class="name">{it.name || "—"}</div>
                        {it.detail && <div class="detail">{it.detail}</div>}
                      </td>
                      <td class="num">{bytes(it.down)}</td>
                      <td class="num">{bytes(it.up)}</td>
                      <td class="num strong">{bytes(sum)}</td>
                      <td class="bar-col">
                        <span class="bar-track">
                          {it.down > 0 && <span class="bar-fill s-down" style={{ width: `${Math.max(0.4, (it.down / max) * 100)}%` }} />}
                          {it.up > 0 && <span class="bar-fill s-up" style={{ width: `${Math.max(0.4, (it.up / max) * 100)}%` }} />}
                        </span>
                      </td>
                      <td class="num">{percent(sum, grand)}</td>
                      <td class="num">{count(it.conns)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {report && !items.length && <div class="empty">{debounced ? "没有匹配的结果" : "这段时间没有流量"}</div>}
            {itemCount > items.length && <div class="empty small">只显示前 {items.length} 个，可搜索查找其余的</div>}
          </div>
        </Card>
        <aside class="detail-panel">
          {selected && report ? (
            <>
              <Card title={selected} extra={<button type="button" class="ghost small" onClick={() => select(selected)}>取消选择</button>}>
                <div class="mini-stats">
                  <div>
                    <span class="muted">下载</span>
                    <strong>{bytes(report.down)}</strong>
                  </div>
                  <div>
                    <span class="muted">上传</span>
                    <strong>{bytes(report.up)}</strong>
                  </div>
                  <div>
                    <span class="muted">连接</span>
                    <strong>{count(report.conns)}</strong>
                  </div>
                </div>
                <Legend />
                <ColumnChart points={report.series} bucket={report.bucket} height={170} />
              </Card>
              <Card title={isApps ? `访问的${filters.groupDomain ? "域名" : "地址"}（${report.hostCount}）` : `访问它的应用（${report.appCount}）`}>
                <BarList
                  items={(isApps ? report.hosts : report.apps).slice(0, 40)}
                  total={isApps ? report.hostsTotal : report.appsTotal}
                  onSelect={(name) => {
                    setFilters(isApps ? { host: name } : { app: name });
                    props.go(isApps ? "hosts" : "apps");
                  }}
                />
              </Card>
              <button type="button" class="ghost" onClick={() => props.go("records")}>
                查看{isApps ? "它的" : "这个地址的"}访问记录 ›
              </button>
              <Card title="经过的节点">
                <BarList items={report.chains} total={report.chainsTotal} labelOf={chainLabel} />
              </Card>
            </>
          ) : (
            <div class="placeholder">
              {isApps ? "选择一个应用，查看它访问了哪些地址、经过哪些节点。" : "选择一个地址，查看哪些应用访问了它。"}
            </div>
          )}
        </aside>
      </div>
    </>
  );
}
