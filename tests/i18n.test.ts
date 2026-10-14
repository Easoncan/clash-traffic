import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { DICTS, LANGUAGES, matchLang, setLang, t, tn, type Lang, type Plural } from "../src/lib/i18n";
import { nameLabel, searchAliases } from "../src/lib/names";

/** The source files of the pages, but the generated client. */
function sources(dir = "src"): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) return f === "locales" ? [] : sources(p);
    return /\.tsx?$/.test(f) && p !== join("src", "mygo.ts") ? [p] : [];
  });
}

/** The keys the code gives t(), tn() and tx() as literals. */
function literalKeys(): Set<string> {
  const keys = new Set<string>();
  for (const f of sources()) {
    const code = readFileSync(f, "utf8");
    for (const m of code.matchAll(/\bt[nx]?\(\s*"((?:[^"\\]|\\.)*)"/g)) keys.add(JSON.parse(`"${m[1]}"`));
  }
  return keys;
}

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
const forms = (e: string | Plural) => (typeof e === "string" ? [e] : Object.values(e).filter((x): x is string => !!x));

// Simplified Chinese has every text, the labels of tables included: it is
// the list of keys the other languages are held to.
const keys = Object.keys(DICTS["zh-Hans"]);

describe("translations", () => {
  test("every text the code shows has a key", () => {
    const missing = [...literalKeys()].filter((k) => !(k in DICTS["zh-Hans"]));
    expect(missing).toEqual([]);
  });

  for (const { id } of LANGUAGES.filter((l) => l.id !== "en")) {
    test(`${id} has every text, with the placeholders of the English`, () => {
      const dict = DICTS[id];
      expect(keys.filter((k) => !(k in dict))).toEqual([]);
      expect(Object.keys(dict).filter((k) => !keys.includes(k))).toEqual([]);
      for (const k of keys) {
        const e = dict[k]!;
        if (typeof e === "string") {
          expect([k, placeholders(e)]).toEqual([k, placeholders(k)]);
        } else {
          // A form may leave the count out, as "every minute" does.
          for (const f of forms(e)) expect([k, placeholders(f).every((p) => placeholders(k).includes(p))]).toEqual([k, true]);
          expect([k, e.other.length > 0]).toEqual([k, true]);
        }
      }
    });
  }

  test("English plurals are of texts that exist", () => {
    expect(Object.keys(DICTS.en).filter((k) => !keys.includes(k))).toEqual([]);
  });
});

describe("matchLang", () => {
  const cases: [string, Lang][] = [
    ["zh-Hans-CN", "zh-Hans"],
    ["zh-CN", "zh-Hans"],
    ["zh", "zh-Hans"],
    ["zh-Hant-TW", "zh-Hant"],
    ["zh-TW", "zh-Hant"],
    ["zh_HK", "zh-Hant"],
    ["ja-JP", "ja"],
    ["ko-KR", "ko"],
    ["ru-RU", "ru"],
    ["en-US", "en"],
    ["fr-FR", "en"],
    ["", "en"],
  ];
  for (const [locale, want] of cases) test(locale || "(empty)", () => expect(matchLang(locale)).toBe(want));
});

describe("t and tn", () => {
  test("a missing text shows in English, with its placeholders filled", () => {
    setLang("ja");
    expect(t("No such text {x}", { x: 1 })).toBe("No such text 1");
    setLang("en");
  });
  test("plural forms follow the language", () => {
    setLang("en");
    expect(tn("{n} days", 1)).toBe("1 day");
    expect(tn("{n} days", 5)).toBe("5 days");
    expect(tn("Every {n} minutes", 1)).toBe("Every minute");
    setLang("zh-Hans");
    expect(tn("{n} days", 1)).toBe("1 天");
    setLang("en");
  });
});

describe("names the data keeps in Chinese (review 9, P2-1)", () => {
  test("show in the language of the app", () => {
    setLang("en");
    expect(nameLabel("未识别进程")).toBe("Unidentified process");
    expect(nameLabel("局域网 192.168.1.5")).toBe("LAN 192.168.1.5");
    expect(nameLabel("Google Chrome")).toBe("Google Chrome");
  });
  test("are found by what they show", () => {
    setLang("en");
    expect(searchAliases("unidentified")).toEqual(["未识别进程"]);
    expect(searchAliases("Unknown")).toEqual(["（未知）", "未知订阅"]);
    expect(searchAliases("lan 192.168")).toEqual(["局域网 192.168"]);
    expect(searchAliases("la")).toEqual(["局域网 "]);
    // Any part of what shows, as "LAN 192.168.1.5" (review 10).
    expect(searchAliases("an")).toEqual(["局域网 "]);
    expect(searchAliases("an 192")).toEqual(["局域网 192"]);
    expect(searchAliases("n 1")).toEqual(["局域网 1"]);
    expect(searchAliases("chrome")).toEqual([]);
    expect(searchAliases("")).toEqual([]);
    setLang("ja");
    expect(searchAliases(t("Unidentified process"))).toEqual(["未识别进程"]);
    // In Simplified Chinese the label is the name itself.
    setLang("zh-Hans");
    expect(searchAliases("未识别")).toEqual([]);
    expect(searchAliases("局域网 10.0")).toEqual([]);
    setLang("en");
  });
});
