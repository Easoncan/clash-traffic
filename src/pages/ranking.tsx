import { useEffect, useState } from "preact/hooks";
import { Card, FilterBar, toQuery, type PageProps } from "../app";
import { BarList, ColumnChart, Legend } from "../components/charts";
import { chainLabel } from "./overview";
import { errorText, useReport } from "../lib/hooks";
import { bytes, count, percent } from "../lib/format";
import { t } from "../lib/i18n";
import { nameLabel, searchAliases } from "../lib/names";
import { Traffic } from "../mygo";

/** The table of applications, or of hosts, with what the picked one did. */
export function Ranking({ kind, ...props }: PageProps & { kind: "apps" | "hosts" }) {
  const { filters, setFilters, notify } = props;
  const [search, setSearch] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(search), 250);
    return () => clearTimeout(timer);
  }, [search]);
  const { query, live } = toQuery(filters, { limit: 500, search: debounced, searchAlso: searchAliases(debounced) });
  const { report, loading, error } = useReport(query, live);

  const isApps = kind === "apps";
  const items = (isApps ? report?.apps : report?.hosts) ?? [];
  const itemCount = (isApps ? report?.appCount : report?.hostCount) ?? 0;
  const selected = isApps ? filters.app : filters.host;
  const select = (name: string) => setFilters(isApps ? { app: selected === name ? "" : name } : { host: selected === name ? "" : name });
  // The page's own dimension is listed whole: its share is of the whole too.
  const grand = (isApps ? report?.appsTotal : report?.hostsTotal) ?? 0;
  const max = items.reduce((m, i) => Math.max(m, i.up + i.down), 0) || 1;
  const noun = isApps
    ? { title: "Apps ({n})", search: "Search apps", column: "App" }
    : filters.groupDomain
      ? { title: "Domains ({n})", search: "Search domains", column: "Domain" }
      : { title: "Hosts ({n})", search: "Search hosts", column: "Host" };

  const exportCSV = async () => {
    try {
      const path = await Traffic.export(query);
      if (path) notify(t("Exported to {path}", { path }));
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
            {t("Merge by domain")}
          </label>
        )}
        <button type="button" class="ghost" onClick={exportCSV}>
          {t("Export CSV")}
        </button>
      </FilterBar>
      {error && <div class="banner error">{t("Could not read the data: {error}", { error })}</div>}
      <div class={`split${loading && report ? " refreshing" : ""}`}>
        <Card
          class="fill"
          title={t(noun.title, { n: itemCount })}
          extra={<input type="search" class="search" placeholder={t(noun.search)} value={search} onInput={(e) => setSearch(e.currentTarget.value)} />}
        >
          <div class="table-wrap">
            <table class="table selectable">
              <thead>
                <tr>
                  <th class="rank">#</th>
                  <th>{t(noun.column)}</th>
                  <th class="num">{t("Download")}</th>
                  <th class="num">{t("Upload")}</th>
                  <th class="num">{t("Total")}</th>
                  <th class="bar-col" aria-label={t("Share, as a bar")} />
                  <th class="num">{t("Share")}</th>
                  <th class="num">{t("Connections")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((it, i) => {
                  const sum = it.up + it.down;
                  return (
                    <tr key={it.name} class={selected === it.name ? "selected" : ""} onClick={() => select(it.name)} tabIndex={0} onKeyDown={(e) => e.key === "Enter" && select(it.name)}>
                      <td class="rank">{i + 1}</td>
                      <td class="name-cell" title={it.detail || it.name}>
                        <div class="name">{nameLabel(it.name) || "—"}</div>
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
            {report && !items.length && <div class="empty">{debounced ? t("No matches") : t("No traffic in this period")}</div>}
            {itemCount > items.length && <div class="empty small">{t("Showing the first {n}: search to find the rest", { n: items.length })}</div>}
          </div>
        </Card>
        <aside class="detail-panel">
          {selected && report ? (
            <>
              <Card title={nameLabel(selected)} extra={<button type="button" class="ghost small" onClick={() => select(selected)}>{t("Clear selection")}</button>}>
                <div class="mini-stats">
                  <div>
                    <span class="muted">{t("Download")}</span>
                    <strong>{bytes(report.down)}</strong>
                  </div>
                  <div>
                    <span class="muted">{t("Upload")}</span>
                    <strong>{bytes(report.up)}</strong>
                  </div>
                  <div>
                    <span class="muted">{t("Connections")}</span>
                    <strong>{count(report.conns)}</strong>
                  </div>
                </div>
                <Legend />
                <ColumnChart points={report.series} bucket={report.bucket} height={170} />
              </Card>
              <Card
                title={
                  isApps
                    ? t(filters.groupDomain ? "Domains it visited ({n})" : "Hosts it visited ({n})", { n: report.hostCount })
                    : t("Apps that visited it ({n})", { n: report.appCount })
                }
              >
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
                {isApps ? t("See its access log") : t("See the access log of this host")} ›
              </button>
              <Card title={t("Nodes it went through")}>
                <BarList items={report.chains} total={report.chainsTotal} labelOf={chainLabel} />
              </Card>
            </>
          ) : (
            <div class="placeholder">
              {isApps ? t("Pick an app to see the hosts it visited and the nodes it went through.") : t("Pick a host to see the apps that visited it.")}
            </div>
          )}
        </aside>
      </div>
    </>
  );
}
