import { afterEach, expect, test } from "bun:test";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkReleaseTag, prepareRelease, releaseURL } from "../scripts/release";

const dirs: string[] = [];
afterEach(async () => { for (const dir of dirs.splice(0)) await rm(dir, { recursive: true, force: true }); });

test("release tags must match the embedded version exactly", () => {
  expect(() => checkReleaseTag("0.2.0", "0.2.0")).not.toThrow();
  for (const tag of ["v0.2.0", "0.1.4", "main", "0.2.0/other"]) expect(() => checkReleaseTag("0.2.0", tag)).toThrow();
});

test("bare-tag URLs are immutable and cannot escape the release", () => {
  const base = "https://github.com/Easoncan/clash-traffic/releases/download/";
  expect(releaseURL(base + "v0.2.0/app.tar.gz", "Easoncan/clash-traffic", "0.2.0", "0.2.0")).toBe(base + "0.2.0/app.tar.gz");
  for (const url of [base + "v0.1.4/app.tar.gz", base + "v0.2.0/%2Fother", base + "v0.2.0/%2e%2e", "https://example.com/app.tar.gz"]) {
    expect(() => releaseURL(url, "Easoncan/clash-traffic", "0.2.0", "0.2.0")).toThrow();
  }
});

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "clash-release-test-"));
  dirs.push(root);
  const dir = join(root, "darwin-universal");
  await mkdir(dir);
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const key = publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64");
  const archive = "clash-traffic-0.2.0-darwin-universal.tar.gz";
  const data = Buffer.from("fixture archive");
  await writeFile(join(dir, archive), data);
  await writeFile(join(dir, "Clash Traffic 0.2.0.dmg"), "fixture installer");
  const manifest = {
    version: "0.2.0", url: `https://github.com/Easoncan/clash-traffic/releases/download/v0.2.0/${archive}`,
    size: data.length, signature: sign(null, createHash("sha256").update(data).digest(), privateKey).toString("base64"),
    previous: [{ version: "0.1.4", url: "https://example.com/previous.tar.gz" }],
  };
  await writeFile(join(dir, "update-darwin-universal.json"), JSON.stringify(manifest));
  return { root, dir, key, archive, manifest };
}

test("signed universal archive serves both CPUs and preserves previous archive URLs", async () => {
  const f = await fixture();
  const out = await prepareRelease(f.root, ["darwin-universal"], "0.2.0", "0.2.0", "Easoncan/clash-traffic", f.key);
  for (const target of ["darwin-universal", "darwin-arm64", "darwin-amd64"]) {
    const manifest = JSON.parse(await readFile(join(out, `update-${target}.json`), "utf8"));
    expect(manifest.url).toContain("/download/0.2.0/");
    expect(manifest.signature).toBe(f.manifest.signature);
    expect(manifest.previous).toEqual(f.manifest.previous);
  }
  expect(await readFile(join(out, f.archive), "utf8")).toBe("fixture archive");
});

test("a tampered update or missing installer cannot enter the upload set", async () => {
  for (const failure of ["tamper", "wrong key", "installer", "manifest"]) {
    const f = await fixture();
    if (failure === "tamper") await writeFile(join(f.dir, f.archive), "corrupt archive"); // same size, wrong signature
    if (failure === "wrong key") f.key = generateKeyPairSync("ed25519").publicKey.export({ format: "der", type: "spki" }).subarray(-32).toString("base64");
    if (failure === "installer") await rm(join(f.dir, "Clash Traffic 0.2.0.dmg"));
    if (failure === "manifest") await rm(join(f.dir, "update-darwin-universal.json"));
    await expect(prepareRelease(f.root, ["darwin-universal"], "0.2.0", "0.2.0", "Easoncan/clash-traffic", f.key)).rejects.toThrow();
  }
});
