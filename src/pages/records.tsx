import { useEffect, useRef, useState } from "preact/hooks";
import { Card, FilterBar, type PageProps } from "../app";
import { errorText } from "../lib/hooks";
import { bytes, count, dateTime, duration } from "../lib/format";
import { lang, t, tn } from "../lib/i18n";
import { nameLabel, routeLabel, searchAliases, subLabel } from "../lib/names";
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
    const timer = setTimeout(() => setDebounced(search), 250);
    return () => clearTimeout(timer);
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
          searchAlso: searchAliases(debounced),
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
      {otherDevice && (
        <div class="banner warning">{t("Each computer keeps its own access log: this is the log of this computer ({name}).", { name: self?.name ?? "" })}</div>
      )}
      {error && <div class="banner error">{t("Could not read the access log: {error}", { error })}</div>}
      <Card
        class={loading && page ? "refreshing" : ""}
        title={
          page
            ? `${tn("{n} connections", page.total, { n: count(page.total) })} · ${t("Download {down} · upload {up}", { down: bytes(page.down), up: bytes(page.up) })}`
            : t("Access log")
        }
        extra={
          <input type="search" class="search wide" placeholder={t("Search apps, hosts, IPs, nodes, rules")} value={search} onInput={(e) => setSearch(e.currentTarget.value)} />
        }
      >
        <div class="table-wrap records">
          <table class="table">
            <thead>
              <tr>
                <th class="col-time">{t("Started")}</th>
                <th class="num col-dur">{t("Duration")}</th>
                <th class="col-app">{t("App")}</th>
                <th class="col-host">{t("Host")}</th>
                <th class="col-chain">{t("Node / rule")}</th>
                <th class="num">{t("Download")}</th>
                <th class="num">{t("Upload")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((s) => (
                <Row key={s.id} s={s} onApp={(app) => setFilters({ app })} onHost={(host) => setFilters({ host, groupDomain: false })} />
              ))}
            </tbody>
          </table>
          {page && !items.length && (
            <div class="empty">{debounced || filters.app || filters.host || filters.chain ? t("No matching connections") : t("No connections in this period")}</div>
          )}
          {page && page.total > items.length && (
            <div class="more">
              <button type="button" class="ghost" onClick={() => setLimit(limit + PAGE)}>
                {t("Show {n} more ({total} in all)", { n: Math.min(PAGE, page.total - items.length), total: count(page.total) })}
              </button>
            </div>
          )}
        </div>
        <p class="hint footnote">
          {t("The start, app, target, node and traffic of every connection of this computer. They stay on this computer and are not synced.")}
          {page?.since ? " " + t("The oldest is from {date}.", { date: new Date(page.since).toLocaleDateString(lang()) }) : ""}{" "}
          {t("Connections that start and end between two samples (a second apart) do not show; the bytes of a connection are those last seen.")}
        </p>
      </Card>
    </>
  );
}

function Row({ s, onApp, onHost }: { s: Session; onApp: (app: string) => void; onHost: (host: string) => void }) {
  const end = s.open ? t("Open") : s.cut ? t("After {time} (the app was not running)", { time: dateTime(s.end) }) : dateTime(s.end);
  const detail = [
    s.route === "proxy" && t("Subscription: {value}", { value: subLabel(s.sub) }),
    s.path && t("Path: {value}", { value: s.path }),
    s.process && s.process !== s.app && t("Process: {value}", { value: s.process }),
    s.source && t("Source: {value}", { value: s.source }),
    s.inbound && t("Inbound: {value}", { value: s.inbound }),
    t("Ended: {value}", { value: end }),
  ]
    .filter(Boolean)
    .join("\n");
  return (
    <tr title={detail}>
      <td class="col-time">
        <div>{dateTime(s.start)}</div>
        {s.open && <span class="badge live">{t("Open")}</span>}
        {s.cut && (
          <span class="badge" title={t("The connection was open when the app quit and had ended when it started again: when it ended and its last traffic are unknown.")}>
            {t("Open when the app quit")}
          </span>
        )}
      </td>
      <td class="num col-dur">
        {s.cut ? "≥ " : ""}
        {duration(s.end - s.start)}
      </td>
      <td class="name-cell col-app">
        <button type="button" class="cell-link name" onClick={() => onApp(s.app)}>
          {nameLabel(s.app)}
        </button>
        {s.process && s.process !== s.app && <div class="detail">{s.process}</div>}
      </td>
      <td class="name-cell col-host">
        <button type="button" class="cell-link name" onClick={() => onHost(s.host)}>
          {nameLabel(s.host)}
        </button>
        <div class="detail">
          {s.network.toUpperCase()}
          {s.dest && ` · ${s.dest}`}
        </div>
      </td>
      <td class="name-cell col-chain">
        <div class="name">
          <span class={`route-tag ${s.route}`}>{s.route === "proxy" && s.sub ? nameLabel(s.sub) : routeLabel(s.route)}</span>
          {s.chains || s.chain}
        </div>
        <div class="detail">{s.rule}</div>
      </td>
      <td class="num strong">{bytes(s.down)}</td>
      <td class="num">{bytes(s.up)}</td>
    </tr>
  );
}
