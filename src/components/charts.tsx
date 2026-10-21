import { useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import type { Item, Point, RatePoint } from "../mygo";
import { useWidth } from "../lib/hooks";
import { byteTicks, bytes, clock, dayLabel, hourLabel, percent, rate } from "../lib/format";
import { lang, t, tn } from "../lib/i18n";
import { nameLabel } from "../lib/names";

/** The two series every chart shows, with their palette slots; t() shows
 * their labels. */
export const SERIES = {
  down: { label: "Download", cls: "s-down" },
  up: { label: "Upload", cls: "s-up" },
} as const;

export function Legend({ kind = "rect" }: { kind?: "rect" | "line" }) {
  return (
    <div class="legend" aria-hidden="true">
      {(["down", "up"] as const).map((k) => (
        <span class="legend-item" key={k}>
          <span class={`key key-${kind} ${SERIES[k].cls}`} />
          {t(SERIES[k].label)}
        </span>
      ))}
    </div>
  );
}

function roundedTop(x: number, y: number, w: number, h: number, r: number): string {
  r = Math.max(0, Math.min(r, w / 2, h));
  return `M${x},${y + h}V${y + r}A${r},${r} 0 0 1 ${x + r},${y}H${x + w - r}A${r},${r} 0 0 1 ${x + w},${y + r}V${y + h}Z`;
}

function Tooltip({ x, y, width, children }: { x: number; y: number; width: number; children: ComponentChildren }) {
  const left = Math.min(Math.max(x + 12, 0), Math.max(0, width - 190));
  return (
    <div class="tooltip" style={{ left: `${left}px`, top: `${Math.max(0, y)}px` }}>
      {children}
    </div>
  );
}

function TipRow({ k, value }: { k: "down" | "up"; value: string }) {
  return (
    <div class="tip-row">
      <span class={`key key-line ${SERIES[k].cls}`} />
      <strong>{value}</strong>
      <span class="muted">{t(SERIES[k].label)}</span>
    </div>
  );
}

const PAD = { top: 12, right: 8, bottom: 26, left: 68 };

/** Download and upload of each bucket of time, stacked. */
export function ColumnChart({ points, bucket, height = 230 }: { points: Point[]; bucket: string; height?: number }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const plotW = Math.max(0, width - PAD.left - PAD.right);
  const plotH = height - PAD.top - PAD.bottom;
  const max = points.reduce((m, p) => Math.max(m, p.up + p.down), 0);
  const ticks = byteTicks(max);
  const top = ticks[ticks.length - 1] || 1;
  const band = points.length ? plotW / points.length : 0;
  const barW = Math.min(24, Math.max(2, band * 0.62));
  const base = PAD.top + plotH;
  const h = (v: number) => (v / top) * plotH;
  const label = bucket === "hour" ? hourLabel : dayLabel;
  const every = Math.max(1, Math.ceil(46 / Math.max(band, 1)));
  const hp = hover != null ? points[hover] : undefined;

  return (
    <div class="chart" ref={ref} style={{ height: `${height}px` }} onPointerLeave={() => setHover(null)}>
      {width > 0 && (
        <svg width={width} height={height} role="img" aria-label={t("Traffic over time, as columns")}>
          {ticks.map((t) => (
            <g key={t}>
              <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={base - h(t)} y2={base - h(t)} />
              <text class="axis" x={PAD.left - 8} y={base - h(t)} text-anchor="end" dominant-baseline="middle">
                {bytes(t)}
              </text>
            </g>
          ))}
          {points.map((p, i) => {
            const x = PAD.left + i * band + (band - barW) / 2;
            const dh = h(p.down);
            const uh = h(p.up);
            const gap = dh >= 1 && uh >= 1 ? 2 : 0;
            const dim = hover != null && hover !== i;
            return (
              <g key={p.t} class={dim ? "dim" : undefined}>
                {dh > 0 && (
                  <path class={SERIES.down.cls} d={uh >= 1 ? `M${x},${base - dh}h${barW}v${dh}h${-barW}Z` : roundedTop(x, base - dh, barW, dh, 4)} />
                )}
                {uh > 0 && <path class={SERIES.up.cls} d={roundedTop(x, base - dh - gap - uh, barW, uh, 4)} />}
                {i % every === 0 && (
                  <text class="axis" x={x + barW / 2} y={base + 16} text-anchor="middle">
                    {label(p.t)}
                  </text>
                )}
              </g>
            );
          })}
          <line class="baseline" x1={PAD.left} x2={width - PAD.right} y1={base} y2={base} />
          {points.map((p, i) => (
            <rect
              key={p.t}
              class="hit"
              x={PAD.left + i * band}
              y={PAD.top}
              width={band}
              height={plotH}
              tabIndex={0}
              aria-label={t("{time}: download {down}, upload {up}", { time: label(p.t), down: bytes(p.down), up: bytes(p.up) })}
              onPointerEnter={() => setHover(i)}
              onFocus={() => setHover(i)}
              onBlur={() => setHover(null)}
            />
          ))}
        </svg>
      )}
      {hp && hover != null && (
        <Tooltip x={PAD.left + hover * band + band / 2} y={PAD.top} width={width}>
          <div class="tip-title">{bucket === "hour" ? `${new Date(hp.t).toLocaleDateString(lang())} ${label(hp.t)}` : new Date(hp.t).toLocaleDateString(lang())}</div>
          <TipRow k="down" value={bytes(hp.down)} />
          <TipRow k="up" value={bytes(hp.up)} />
        </Tooltip>
      )}
    </div>
  );
}

/** The speed of the last minutes, as two lines. */
export function RateChart({ points, height = 190, span = 300_000 }: { points: RatePoint[]; height?: number; span?: number }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const plotW = Math.max(0, width - PAD.left - PAD.right);
  const plotH = height - PAD.top - PAD.bottom;
  const end = points.length ? points[points.length - 1]!.t : Date.now();
  const start = end - span;
  const shown = points.filter((p) => p.t >= start);
  const max = shown.reduce((m, p) => Math.max(m, p.up, p.down), 0);
  const ticks = byteTicks(max).filter((_, i, a) => a.length <= 5 || i % 2 === 0 || i === a.length - 1);
  const top = byteTicks(max).at(-1) || 1;
  const base = PAD.top + plotH;
  const x = (t: number) => PAD.left + ((t - start) / span) * plotW;
  const y = (v: number) => base - (v / top) * plotH;
  const line = (k: "up" | "down") => shown.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p[k]).toFixed(1)}`).join("");
  const area = (k: "up" | "down") =>
    shown.length ? `${line(k)}L${x(shown[shown.length - 1]!.t).toFixed(1)},${base}L${x(shown[0]!.t).toFixed(1)},${base}Z` : "";
  const hp = hover != null ? shown[hover] : undefined;
  const minuteTicks: number[] = [];
  for (let t = Math.ceil(start / 60_000) * 60_000; t <= end; t += 60_000) minuteTicks.push(t);

  const onMove = (e: PointerEvent) => {
    if (!shown.length) return;
    const rect = (e.currentTarget as Element).getBoundingClientRect();
    const t = start + ((e.clientX - rect.left - PAD.left) / plotW) * span;
    let best = 0;
    for (let i = 1; i < shown.length; i++) if (Math.abs(shown[i]!.t - t) < Math.abs(shown[best]!.t - t)) best = i;
    setHover(best);
  };

  return (
    <div class="chart" ref={ref} style={{ height: `${height}px` }} onPointerMove={onMove} onPointerLeave={() => setHover(null)}>
      {width > 0 && (
        <svg width={width} height={height} role="img" aria-label={t("Live speed, as lines")}>
          {ticks.map((t) => (
            <g key={t}>
              <line class="grid" x1={PAD.left} x2={width - PAD.right} y1={y(t)} y2={y(t)} />
              <text class="axis" x={PAD.left - 8} y={y(t)} text-anchor="end" dominant-baseline="middle">
                {rate(t)}
              </text>
            </g>
          ))}
          {minuteTicks.map((t) => (
            <text key={t} class="axis" x={x(t)} y={base + 16} text-anchor="middle">
              {clock(t).slice(0, 5)}
            </text>
          ))}
          <path class={`wash ${SERIES.down.cls}`} d={area("down")} />
          <path class={`wash ${SERIES.up.cls}`} d={area("up")} />
          <path class={`stroke ${SERIES.down.cls}`} d={line("down")} />
          <path class={`stroke ${SERIES.up.cls}`} d={line("up")} />
          <line class="baseline" x1={PAD.left} x2={width - PAD.right} y1={base} y2={base} />
          {hp && (
            <g>
              <line class="crosshair" x1={x(hp.t)} x2={x(hp.t)} y1={PAD.top} y2={base} />
              <circle class={`dot ${SERIES.down.cls}`} cx={x(hp.t)} cy={y(hp.down)} r={4} />
              <circle class={`dot ${SERIES.up.cls}`} cx={x(hp.t)} cy={y(hp.up)} r={4} />
            </g>
          )}
        </svg>
      )}
      {hp && (
        <Tooltip x={x(hp.t)} y={PAD.top} width={width}>
          <div class="tip-title">{clock(hp.t)}</div>
          <TipRow k="down" value={rate(hp.down)} />
          <TipRow k="up" value={rate(hp.up)} />
        </Tooltip>
      )}
    </div>
  );
}

/** A ranked list of names with their traffic, one bar each. */
export function BarList({
  items,
  total,
  selected,
  onSelect,
  empty,
  describe,
  labelOf = (i) => nameLabel(i.name) || "—",
  canSelect = () => true,
}: {
  items: Item[];
  total: number;
  selected?: string;
  onSelect?: (name: string) => void;
  empty?: string;
  describe?: (item: Item) => string;
  /** What to show for an item; its name identifies it. */
  labelOf?: (item: Item) => string;
  /** Whether picking an item filters by it. */
  canSelect?: (item: Item) => boolean;
}) {
  if (!items.length) return <div class="empty">{empty ?? t("No data yet")}</div>;
  const max = items.reduce((m, i) => Math.max(m, i.up + i.down), 0) || 1;
  return (
    <ul class="barlist">
      {items.map((it) => {
        const sum = it.up + it.down;
        return (
          <li key={it.name}>
            <button
              type="button"
              class={`bar-row${selected === it.name ? " selected" : ""}`}
              disabled={!onSelect || !canSelect(it)}
              onClick={() => onSelect?.(it.name)}
              title={`${describe ? describe(it) + "\n" : ""}${t("Download {down} · upload {up}", { down: bytes(it.down), up: bytes(it.up) })} · ${tn("{n} connections", it.conns)}`}
            >
              <span class="bar-name">{labelOf(it)}</span>
              <span class="bar-value">{bytes(sum)}</span>
              <span class="bar-share">{percent(sum, total)}</span>
              <span class="bar-track">
                {it.down > 0 && <span class="bar-fill s-down" style={{ width: `${Math.max(0.4, (it.down / max) * 100)}%` }} />}
                {it.up > 0 && <span class="bar-fill s-up" style={{ width: `${Math.max(0.4, (it.up / max) * 100)}%` }} />}
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
