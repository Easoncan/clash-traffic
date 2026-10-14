import type { ComponentChildren } from "preact";
import en from "../locales/en";
import zhHans from "../locales/zh-Hans";
import zhHant from "../locales/zh-Hant";
import ja from "../locales/ja";
import ko from "../locales/ko";
import ru from "../locales/ru";

export type Lang = "en" | "zh-Hans" | "zh-Hant" | "ja" | "ko" | "ru";

/** The forms of a text that counts something, by Intl.PluralRules category. */
export type Plural = Partial<Record<Intl.LDMLPluralRule, string>> & { other: string };

/**
 * The texts of a language, by their English text: missing ones show in
 * English. Placeholders are {name}; a text that counts something takes the
 * count as {n} and may give each plural form.
 */
export type Dict = Record<string, string | Plural>;

/** The languages, each named in itself. */
export const LANGUAGES: { id: Lang; name: string }[] = [
  { id: "en", name: "English" },
  { id: "zh-Hans", name: "简体中文" },
  { id: "zh-Hant", name: "繁體中文" },
  { id: "ja", name: "日本語" },
  { id: "ko", name: "한국어" },
  { id: "ru", name: "Русский" },
];

export const DICTS: Record<Lang, Dict> = { en, "zh-Hans": zhHans, "zh-Hant": zhHant, ja, ko, ru };

let current: Lang = "en";

export function lang(): Lang {
  return current;
}

export function setLang(l: Lang) {
  current = DICTS[l] ? l : "en";
  if (typeof document !== "undefined") document.documentElement.lang = current;
}

/** The language of the app for a locale such as zh-Hans-CN, zh-TW or en-US. */
export function matchLang(locale: string): Lang {
  const tag = locale.replace(/_/g, "-").toLowerCase();
  const [base] = tag.split("-");
  if (base === "zh") {
    return /-(hant|tw|hk|mo)\b/.test(tag) ? "zh-Hant" : "zh-Hans";
  }
  return (["ja", "ko", "ru"] as const).find((l) => l === base) ?? "en";
}

function fill(text: string, params?: Record<string, string | number>): string {
  if (!params) return text;
  return text.replace(/\{(\w+)\}/g, (m, k: string) => (k in params ? String(params[k]) : m));
}

function entry(key: string): string | Plural {
  return DICTS[current][key] ?? DICTS.en[key] ?? key;
}

/** The text of key, the English text, in the current language. */
export function t(key: string, params?: Record<string, string | number>): string {
  const e = entry(key);
  return fill(typeof e === "string" ? e : e.other, params);
}

/** The text of key for a count of n, given to it as {n}. */
export function tn(key: string, n: number, params?: Record<string, string | number>): string {
  const e = entry(key);
  const form = typeof e === "string" ? e : (e[new Intl.PluralRules(current).select(n)] ?? e.other);
  return fill(form, { n: n.toLocaleString(current), ...params });
}

/** The text of key with elements, such as <code>, for its placeholders. */
export function tx(key: string, params: Record<string, ComponentChildren>): ComponentChildren[] {
  const e = entry(key);
  const text = typeof e === "string" ? e : e.other;
  return text.split(/(\{\w+\})/).map((part) => {
    const name = /^\{(\w+)\}$/.exec(part)?.[1];
    return name !== undefined && name in params ? params[name] : part;
  });
}
