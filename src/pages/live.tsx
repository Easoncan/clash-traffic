import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { Card, RouteSwitch, type PageProps } from "../app";
import { Legend, RateChart } from "../components/charts";
import { errorText } from "../lib/hooks";
import { bytes, duration, rate } from "../lib/format";
import { t } from "../lib/i18n";
import { nameLabel, routeLabel, searchAliases } from "../lib/names";
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
    if (q) {
      const terms = [q, ...searchAliases(q).map((a) => a.toLowerCase())];
      list = list.filter((c) => [c.app, c.process, c.host, c.dest, c.chains, c.rule].some((s) => terms.some((term) => s.toLowerCase().includes(term))));
    }
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
      {!status?.connected && <div class="banner error">{t("Not connected to Clash: {error}", { error: status?.error || t("Searching…") })}</div>}
      <div class="hero-row">
        <div class="stat">
          <div class="stat-label">
            <span class="key key-line s-down" aria-hidden="true" />
            {t("Download speed")}
          </div>
          <div class="stat-value">{rate(live?.downRate ?? 0)}</div>
        </div>
        <div class="stat">
          <div class="stat-label">
            <span class="key key-line s-up" aria-hidden="true" />
            {t("Upload speed")}
          </div>
          <div class="stat-value">{rate(live?.upRate ?? 0)}</div>
        </div>
        <div class="stat">
          <div class="stat-label">{t("Open connections")}</div>
          <div class="stat-value">{live?.total ?? 0}</div>
        </div>
        <div class="stat">
          <div class="stat-label">{t("Since the core started")}</div>
          <div class="stat-value small">
            {t("Download {bytes}", { bytes: bytes(live?.coreDown ?? 0) })}
            <br />
            {t("Upload {bytes}", { bytes: bytes(live?.coreUp ?? 0) })}
          </div>
        </div>
      </div>
      <Card title={t("Last 5 minutes")} extra={<Legend kind="line" />}>
        <RateChart points={rateState.rates} />
      </Card>
      <Card
        title={t("Open connections ({n})", { n: needAll || !live ? conns.length : live.total })}
        extra={
          <div class="card-tools">
            <RouteSwitch value={filters.route} onChange={(route) => setFilters({ route })} />
            <label class="switch">
              <input type="checkbox" checked={byApp} onChange={(e) => setByApp(e.currentTarget.checked)} />
              {t("Group by app")}
            </label>
            <select value={sort} onChange={(e) => setSort(e.currentTarget.value as Sort)} aria-label={t("Sort")}>
              <option value="speed">{t("By speed")}</option>
              <option value="total">{t("By traffic")}</option>
              <option value="start">{t("By start time")}</option>
            </select>
            <input type="search" class="search" placeholder={t("Search apps, hosts, nodes, rules")} value={search} onInput={(e) => setSearch(e.currentTarget.value)} />
          </div>
        }
      >
        <div class="table-wrap tall">
          {byApp ? (
            <table class="table">
              <thead>
                <tr>
                  <th>{t("App")}</th>
                  <th class="num">{t("Connections")}</th>
                  <th class="num">{t("Download speed")}</th>
                  <th class="num">{t("Upload speed")}</th>
                  <th class="num">{t("Downloaded")}</th>
                  <th class="num">{t("Uploaded")}</th>
                </tr>
              </thead>
              <tbody>
                {groups.map((g) => (
                  <tr key={g.app}>
                    <td class="name-cell">
                      <div class="name">{nameLabel(g.app)}</div>
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
                  <th class="col-app">{t("App")}</th>
                  <th class="col-host">{t("Host")}</th>
                  <th class="col-chain">{t("Node / rule")}</th>
                  <th class="num">{t("Download speed")}</th>
                  <th class="num">{t("Upload speed")}</th>
                  <th class="num">{t("Downloaded")}</th>
                  <th class="num">{t("Uploaded")}</th>
                  <th class="num">{t("Duration")}</th>
                  <th aria-label={t("Actions")} />
                </tr>
              </thead>
              <tbody>
                {conns.map((c) => (
                  <tr key={c.id}>
                    <td class="name-cell col-app" title={c.process}>
                      <div class="name">{nameLabel(c.app)}</div>
                      {c.process && c.process !== c.app && <div class="detail">{c.process}</div>}
                    </td>
                    <td class="name-cell col-host" title={c.host}>
                      <div class="name">{nameLabel(c.host)}</div>
                      <div class="detail">
                        {c.network.toUpperCase()}
                        {c.dest && ` · ${c.dest}`}
                      </div>
                    </td>
                    <td class="name-cell col-chain" title={c.chains}>
                      <div class="name">
                        <span class={`route-tag ${c.route}`}>{c.route === "proxy" && c.sub ? nameLabel(c.sub) : routeLabel(c.route)}</span>
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
                      <button type="button" class="icon-btn" title={t("Close this connection")} aria-label={t("Close the connection")} onClick={() => close(c.id)}>
                        ×
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {live && !conns.length && <div class="empty">{search ? t("No matching connections") : t("No open connections")}</div>}
          {live && !needAll && live.total > conns.length && (
            <div class="empty small">
              {t("Listing the fastest {n} only; searching, sorting otherwise or filtering lists all {total}", { n: conns.length, total: live.total })}
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
