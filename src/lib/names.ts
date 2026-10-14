import { t } from "./i18n";

/** The routes, in English: t() shows them. */
const ROUTE_LABEL: Record<string, string> = { proxy: "Proxy", direct: "Direct", reject: "Blocked", unknown: "Unknown" };

export function routeLabel(route: string): string {
  return ROUTE_LABEL[route] ? t(ROUTE_LABEL[route]) : route;
}

// What the collector calls what it cannot name, kept in the data as it is,
// in Chinese, for every device: shown in the language of the app.
const UNNAMED: Record<string, string> = {
  未识别进程: "Unidentified process",
  "短连接（未捕获）": "Short connections (not captured)",
  "（未知）": "(unknown)",
  未知订阅: "Unknown subscription",
};
const LAN_PREFIX = "局域网 ";

/** An app, host or subscription as the data names it, for showing. */
export function nameLabel(name: string): string {
  if (UNNAMED[name]) return t(UNNAMED[name]);
  if (name.startsWith(LAN_PREFIX)) return t("LAN {host}", { host: name.slice(LAN_PREFIX.length) });
  return name;
}

/** How a subscription reads; "" is the traffic of none. */
export function subLabel(sub: string): string {
  return sub === "" ? t("Direct (no subscription)") : nameLabel(sub);
}

/**
 * The names, as the data spells them, that a search for text finds through
 * their labels: "unidentified" finds 未识别进程 in English, "LAN 192.168"
 * finds 局域网 192.168.….
 */
export function searchAliases(text: string): string[] {
  const q = text.trim().toLowerCase();
  if (!q) return [];
  const out: string[] = [];
  for (const [raw, key] of Object.entries(UNNAMED)) {
    if (t(key).toLowerCase().includes(q) && !raw.toLowerCase().includes(q)) out.push(raw);
  }
  // Where the label puts the address last, as every language does now: a
  // search within the label's prefix finds every LAN name, one that runs
  // from the prefix into the address finds those addresses.
  const [before, after] = t("LAN {host}").split("{host}");
  const prefix = (before ?? "").toLowerCase();
  if (prefix.trim() && after === "" && prefix !== LAN_PREFIX.toLowerCase()) {
    if (prefix.includes(q)) {
      out.push(LAN_PREFIX);
    } else {
      for (let k = Math.min(q.length, prefix.length); k > 0; k--) {
        if (prefix.endsWith(q.slice(0, k))) {
          out.push(LAN_PREFIX + q.slice(k));
          break;
        }
      }
    }
  }
  return out;
}
