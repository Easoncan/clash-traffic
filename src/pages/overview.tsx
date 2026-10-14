import { useState } from "preact/hooks";
import { Card, FilterBar, subLabel, toQuery, type PageProps } from "../app";
import { BarList, ColumnChart, Legend } from "../components/charts";
import { useReport } from "../lib/hooks";
import { bytes, count, dayLabel, hourLabel, osName, percent } from "../lib/format";
import { t, tn, tx } from "../lib/i18n";
import { rangeLabel } from "../lib/range";
import type { Report } from "../mygo";

// As the collector names it in the data.
const UNKNOWN_APPS = new Set(["未识别进程"]);

export function Overview(props: PageProps) {
  const { filters, setFilters, go, status } = props;
  const { query, live } = toQuery(filters, { limit: 8 });
  const { report, loading, error } = useReport(query, live);
  const [table, setTable] = useState(false);
  const total = report ? report.up + report.down : 0;
  const unknown = report?.apps.filter((a) => UNKNOWN_APPS.has(a.name)).reduce((s, a) => s + a.up + a.down, 0) ?? 0;

  return (
    <>
      <FilterBar filters={filters} setFilters={setFilters} />
      {error && <div class="banner error">{t("Could not read the data: {error}", { error })}</div>}
      {status?.connected && status.findProcessMode !== "always" && total > 0 && unknown / total > 0.2 && (
        <div class="banner warning">
          <strong>{t("{share} of the traffic is not matched to an app.", { share: percent(unknown, total) })}</strong>{" "}
          {tx("Clash's find-process-mode is {mode}: it only identifies processes when a rule needs them.", { mode: <code>{status.findProcessMode || t("Unknown")}</code> })}{" "}
          <button type="button" class="link" onClick={() => go("settings")}>
            {t("Turn on process identification in Settings")}
          </button>
        </div>
      )}
      <div class={`stack${loading && report ? " refreshing" : ""}`}>
        <div class="hero-row">
          <div class="hero">
            <div class="hero-label">{t("Total traffic, {range}", { range: rangeLabel(filters.range) })}</div>
            <div class="hero-value">{bytes(total)}</div>
          </div>
          <Stat label={t("Download")} value={bytes(report?.down ?? 0)} k="s-down" />
          <Stat label={t("Upload")} value={bytes(report?.up ?? 0)} k="s-up" />
          <Stat label={t("Connections")} value={count(report?.conns ?? 0)} />
          <Stat
            label={t("Proxy traffic")}
            value={bytes(routeTotal(report, "proxy"))}
            sub={t("Direct {bytes}", { bytes: bytes(routeTotal(report, "direct")) })}
            onClick={() => setFilters({ route: filters.route === "proxy" ? "" : "proxy" })}
            active={filters.route === "proxy"}
          />
        </div>

        <Card
          title={t("Traffic over time")}
          extra={
            <div class="card-tools">
              <Legend />
              <button type="button" class="ghost small" onClick={() => setTable(!table)}>
                {table ? t("Chart") : t("Table")}
              </button>
            </div>
          }
        >
          {report && (table ? <SeriesTable report={report} /> : <ColumnChart points={report.series} bucket={report.bucket} />)}
        </Card>

        <div class="grid-2">
          <Card title={t("Apps")} extra={<MoreLink onClick={() => go("apps")} n={report?.appCount ?? 0} />}>
            <BarList items={report?.apps ?? []} total={report?.appsTotal ?? 0} selected={filters.app} onSelect={(app) => setFilters({ app: filters.app === app ? "" : app })} describe={(i) => i.detail} />
          </Card>
          <Card title={filters.groupDomain ? t("Domains") : t("Hosts")} extra={<MoreLink onClick={() => go("hosts")} n={report?.hostCount ?? 0} />}>
            <BarList items={report?.hosts ?? []} total={report?.hostsTotal ?? 0} selected={filters.host} onSelect={(host) => setFilters({ host: filters.host === host ? "" : host })} />
          </Card>
          <Card title={t("Proxy nodes")}>
            <BarList
              items={report?.chains ?? []}
              total={report?.chainsTotal ?? 0}
              selected={filters.chain}
              labelOf={chainLabel}
              canSelect={(i) => i.name !== ""}
              onSelect={(chain) => setFilters({ chain: filters.chain === chain ? "" : chain })}
            />
          </Card>
          <Card title={t("Subscriptions")} extra={<MoreLink onClick={() => go("subs")} />}>
            <BarList
              items={report?.subs ?? []}
              total={report?.subsTotal ?? 0}
              selected={filters.sub ?? undefined}
              labelOf={(i) => subLabel(i.name)}
              onSelect={(sub) => setFilters({ sub: filters.sub === sub ? null : sub })}
            />
          </Card>
          <Card title={t("Devices")} extra={<MoreLink onClick={() => go("devices")} />}>
            <BarList
              items={(report?.devices ?? []).map((d) => ({ name: d.device.id, up: d.up, down: d.down, conns: d.conns, detail: "" }))}
              total={(report?.devices ?? []).reduce((s, d) => s + d.up + d.down, 0)}
              selected={filters.device}
              onSelect={(id) => setFilters({ device: filters.device === id ? "" : id })}
              labelOf={(i) => {
                const d = report?.devices.find((x) => x.device.id === i.name);
                return d ? `${d.device.name}${d.self ? t(" (this computer)") : ""}` : i.name;
              }}
              describe={(i) => osName(report?.devices.find((x) => x.device.id === i.name)?.device.os ?? "")}
            />
          </Card>
        </div>
      </div>
    </>
  );
}

/** The node of bytes no connection could be blamed for has no name. */
export function chainLabel(i: { name: string }): string {
  return i.name || t("— (short connections, unattributed)");
}

/** The traffic of a route, whatever route the page shows. */
function routeTotal(report: Report | null, route: string): number {
  const it = report?.routes.find((r) => r.name === route);
  return it ? it.up + it.down : 0;
}

function Stat({ label, value, k, sub, onClick, active }: { label: string; value: string; k?: string; sub?: string; onClick?: () => void; active?: boolean }) {
  const body = (
    <>
      <div class="stat-label">
        {k && <span class={`key key-rect ${k}`} aria-hidden="true" />}
        {label}
      </div>
      <div class="stat-value">{value}</div>
      {sub && <div class="stat-sub">{sub}</div>}
    </>
  );
  if (!onClick) return <div class="stat">{body}</div>;
  return (
    <button type="button" class={`stat clickable${active ? " active" : ""}`} onClick={onClick} title={active ? t("Show all traffic") : t("Show proxy traffic only")}>
      {body}
    </button>
  );
}

function MoreLink({ onClick, n }: { onClick: () => void; n?: number }) {
  return (
    <button type="button" class="link" onClick={onClick}>
      {n ? tn("All {n}", n) : t("View")} ›
    </button>
  );
}

function SeriesTable({ report }: { report: Report }) {
  const label = report.bucket === "hour" ? hourLabel : dayLabel;
  return (
    <div class="table-wrap short">
      <table class="table">
        <thead>
          <tr>
            <th>{report.bucket === "hour" ? t("Time") : t("Date")}</th>
            <th class="num">{t("Download")}</th>
            <th class="num">{t("Upload")}</th>
            <th class="num">{t("Total")}</th>
          </tr>
        </thead>
        <tbody>
          {report.series
            .filter((p) => p.up + p.down > 0)
            .map((p) => (
              <tr key={p.t}>
                <td>{label(p.t)}</td>
                <td class="num">{bytes(p.down)}</td>
                <td class="num">{bytes(p.up)}</td>
                <td class="num">{bytes(p.up + p.down)}</td>
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
}
