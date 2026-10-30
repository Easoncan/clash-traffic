import { createHash, createPublicKey, verify } from "node:crypto";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import config from "../mygo.config";

type SignedFile = { url: string; size: number; signature: string };
type Manifest = SignedFile & { version: string; deltas?: SignedFile[] };

export function checkReleaseTag(version: string, tag: string) {
  if (!/^\d+\.\d+\.\d+$/.test(tag) || tag !== version) {
    throw new Error(`Release tag ${JSON.stringify(tag)} must equal config version ${version}, without a v prefix`);
  }
}

/** MyGo 0.4.8 hardcodes v+version for archive URLs; this fork uses bare tags. */
export function releaseURL(url: string, repository: string, version: string, tag: string) {
  const prefix = `https://github.com/${repository}/releases/download/`;
  const parsed = new URL(url);
  const source = [`${prefix}v${version}/`, `${prefix}${tag}/`].find((p) => url.startsWith(p));
  if (!source) {
    throw new Error(`Unexpected update archive URL: ${url}`);
  }
  const name = decodeURIComponent(url.slice(source.length));
  if (!name || name.includes("/") || name.includes("\\") || name === "." || name === ".." || parsed.search || parsed.hash) {
    throw new Error(`Invalid update archive filename: ${url}`);
  }
  return `${prefix}${tag}/${encodeURIComponent(name)}`;
}

export async function prepareRelease(root: string, targets: string[], tag: string, version: string, repository: string, publicKey: string) {
  checkReleaseTag(version, tag);
  const key = createPublicKey({
    key: Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), Buffer.from(publicKey, "base64")]),
    format: "der", type: "spki",
  });
  const staged = new Map<string, Buffer>();
  for (const target of targets) {
    if (!/^(darwin-(universal|arm64|amd64)|(?:linux|windows)-(?:arm64|amd64))$/.test(target)) {
      throw new Error(`Unsupported release target: ${target}`);
    }
    const dir = join(root, target);
    const manifest: Manifest = JSON.parse(await readFile(join(dir, `update-${target}.json`), "utf8"));
    if (manifest.version !== version) throw new Error(`Wrong version in ${target}: ${manifest.version}`);
    if (!new URL(manifest.url).pathname.endsWith(`-${version}-${target}.tar.gz`)) throw new Error(`Wrong archive target: ${manifest.url}`);
    for (const file of [manifest, ...(manifest.deltas ?? [])]) {
      file.url = releaseURL(file.url, repository, version, tag);
      const name = decodeURIComponent(basename(new URL(file.url).pathname));
      const data = await readFile(join(dir, name));
      const digest = createHash("sha256").update(data).digest();
      if (data.length !== file.size || !verify(null, digest, key, Buffer.from(file.signature, "base64"))) {
        throw new Error(`Update size/signature check failed: ${target}/${name}`);
      }
      staged.set(name, data);
    }
    const names = (await readdir(dir, { withFileTypes: true })).filter((f) => f.isFile()).map((f) => f.name);
    const installers = names.filter((name) => target.startsWith("darwin-") ? name.endsWith(".dmg")
      : target.startsWith("windows-") ? name.includes(" Setup ") && name.endsWith(".exe")
      : name.endsWith(".deb"));
    if (installers.length !== 1 || !installers[0]!.includes(version)) {
      throw new Error(`Missing or ambiguous ${version} installer for ${target}`);
    }
    for (const name of [...installers, ...(target.startsWith("linux-") ? ["install.sh"] : [])]) {
      const data = await readFile(join(dir, name));
      if (staged.has(name) && !staged.get(name)!.equals(data)) throw new Error(`Conflicting release asset: ${name}`);
      staged.set(name, data);
    }
    const data = Buffer.from(JSON.stringify(manifest, null, 2) + "\n");
    staged.set(`update-${target}.json`, data);
    if (target === "darwin-universal") {
      // Older MyGo apps ask for their running CPU's feed, even in a universal app.
      staged.set("update-darwin-arm64.json", data);
      staged.set("update-darwin-amd64.json", data);
    }
  }
  const out = join(root, "assets");
  // Validate every input before creating the upload set. A fresh build root is
  // required so an interrupted or older build cannot contribute stale assets.
  await mkdir(out);
  for (const [name, data] of staged) await writeFile(join(out, name), data);
  return out;
}

if (import.meta.main) {
  const args = process.argv.slice(2);
  const platforms = args[0];
  const tag = process.env.GITHUB_REF_NAME || config.version;
  checkReleaseTag(config.version, tag);
  if (!platforms || args.length !== 1) throw new Error("Usage: bun run build:release darwin/universal,linux/amd64,...");
  if (!process.env.MYGO_UPDATER_PRIVATE_KEY) throw new Error("MYGO_UPDATER_PRIVATE_KEY is required for release builds");
  const out = resolve("build", `release-${platforms.replaceAll(/[\/,]/g, "-")}`);
  // Never combine files left by earlier releases with the new upload set.
  await rm(out, { recursive: true, force: true });
  const proc = Bun.spawn(["bun", "run", "build", "-platform", platforms, "-o", out], { stdout: "inherit", stderr: "inherit" });
  if (await proc.exited !== 0) throw new Error("MyGo release build failed");
  const assets = await prepareRelease(out, platforms.split(",").map((p) => p.replace("/", "-")), tag, config.version, config.updates.github, config.updates.publicKey);
  console.log(`Verified release assets: ${assets}`);
}
