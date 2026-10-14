import { useEffect, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { Card, type PageProps } from "../app";
import { errorText } from "../lib/hooks";
import { Monitor, Preferences, type AboutInfo, type ClashStatus, type Endpoint, type Settings } from "../mygo";

export function SettingsPage({ status, notify }: PageProps) {
  const [saved, setSaved] = useState<Settings | null>(null);
  const [form, setForm] = useState<Settings | null>(null);
  const [password, setPassword] = useState<string | null>(null);
  const [about, setAbout] = useState<AboutInfo | null>(null);
  const [candidates, setCandidates] = useState<Endpoint[] | null>(null);
  const [clashTest, setClashTest] = useState<ClashStatus | null>(null);
  const [syncTest, setSyncTest] = useState<{ ok: boolean; msg: string } | null>(null);
  const [busy, setBusy] = useState("");

  useEffect(() => {
    Preferences.get().then((s) => (setSaved(s), setForm(s))).catch((err) => notify(errorText(err), "error"));
    Preferences.about().then(setAbout).catch(() => {});
  }, []);
  if (!form || !saved) return null;

  const set = (patch: Partial<Settings>) => setForm({ ...form, ...patch });
  const dirty = JSON.stringify(form) !== JSON.stringify(saved) || password !== null;
  const isMac = document.documentElement.dataset.platform === "darwin";

  const run = async (name: string, fn: () => Promise<void>) => {
    setBusy(name);
    try {
      await fn();
    } catch (err) {
      notify(errorText(err), "error");
    } finally {
      setBusy("");
    }
  };
  const save = () =>
    run("save", async () => {
      try {
        const s = await Preferences.save(form, password);
        setSaved(s);
        setForm(s);
        setPassword(null);
        notify("设置已保存");
      } catch (err) {
        // Saved, but something did not apply, such as starting at login.
        const s = await Preferences.get();
        setSaved(s);
        setForm(s);
        throw err;
      }
    });
  const testClash = () => run("clash", async () => setClashTest(await Preferences.testClash(form.clashMode, form.clashAddress, form.clashSecret)));
  const testSync = () =>
    run("sync", async () => {
      try {
        await Preferences.testSync(form, password);
        setSyncTest({ ok: true, msg: "读写正常" });
      } catch (err) {
        setSyncTest({ ok: false, msg: errorText(err) });
      }
    });
  const chooseFolder = () =>
    run("folder", async () => {
      const dir = await Preferences.chooseFolder();
      if (dir) set({ syncFolder: dir });
    });

  return (
    <div class="settings">
      <Card title="Clash 连接">
        <div class="status-box">
          <span class={`status-dot ${status?.connected ? "good" : "critical"}`} aria-hidden="true" />
          <div>
            <strong>{status?.connected ? `已连接 · mihomo ${status.version}` : "未连接"}</strong>
            <div class="muted small">{status?.connected ? `${status.endpoint}（来源：${status.source}）` : status?.error}</div>
          </div>
          <button type="button" class="ghost small" onClick={() => Monitor.reconnect()}>
            重新连接
          </button>
        </div>
        <Field label="连接方式">
          <div class="segmented">
            <button type="button" class={form.clashMode === "auto" ? "on" : ""} onClick={() => set({ clashMode: "auto" })}>
              自动检测 Clash Verge
            </button>
            <button type="button" class={form.clashMode === "manual" ? "on" : ""} onClick={() => set({ clashMode: "manual" })}>
              手动填写
            </button>
          </div>
        </Field>
        {form.clashMode === "manual" ? (
          <>
            <Field label="控制接口地址" hint={"例如 http://127.0.0.1:9097、unix:///tmp/verge/verge-mihomo.sock 或 \\\\.\\pipe\\verge-mihomo"}>
              <input type="text" value={form.clashAddress} placeholder="http://127.0.0.1:9097" onInput={(e) => set({ clashAddress: e.currentTarget.value })} />
            </Field>
            <Field label="密钥 (secret)" hint="Clash Verge 设置 → 外部控制 中的密钥；使用 socket / 管道时不需要">
              <input type="password" value={form.clashSecret} onInput={(e) => set({ clashSecret: e.currentTarget.value })} />
            </Field>
          </>
        ) : (
          <Field label="检测顺序" hint="依次尝试：运行中的内核命令行 → Clash Verge 配置文件 → 默认地址">
            {candidates ? (
              <ul class="plain-list">
                {candidates.map((c) => (
                  <li key={c.address}>
                    <code>{c.kind === "unix" ? `unix://${c.address}` : c.address}</code> <span class="muted">（{c.source}）</span>
                  </li>
                ))}
                {!candidates.length && <li class="muted">没有找到候选地址，Clash Verge 可能没有运行</li>}
              </ul>
            ) : (
              <button type="button" class="link" onClick={() => Monitor.candidates().then(setCandidates)}>
                查看候选地址
              </button>
            )}
          </Field>
        )}
        <div class="row">
          <button type="button" class="ghost" disabled={busy === "clash"} onClick={testClash}>
            {busy === "clash" ? "测试中…" : "测试连接"}
          </button>
          {clashTest && (
            <span class={clashTest.connected ? "ok-text" : "error-text"}>
              {clashTest.connected ? `✓ 连接成功：${clashTest.endpoint}（mihomo ${clashTest.version}）` : `✕ ${clashTest.error}`}
            </span>
          )}
        </div>
        <Toggle checked={form.forceProcess} onChange={(v) => set({ forceProcess: v })} label="自动开启进程识别（统计每个应用的流量必须开启）">
          Clash Verge 默认的 <code>find-process-mode: strict</code> 只在规则用到进程时才识别进程，大部分连接因此不知道来自哪个应用。开启后，本应用会把运行中内核的模式设为 <code>always</code>，Clash Verge 重新加载配置后会自动再设一次。
          当前内核：<code>{status?.findProcessMode || "未知"}</code>。若想永久生效，可在 Clash Verge「订阅 → 全局扩展覆写配置」中加入 <code>find-process-mode: always</code>。
        </Toggle>
      </Card>

      <Card title="本机">
        <Field label="设备名称" hint="在多设备统计中显示">
          <input type="text" value={form.deviceName} onInput={(e) => set({ deviceName: e.currentTarget.value })} />
        </Field>
        <Toggle checked={form.openAtLogin} onChange={(v) => set({ openAtLogin: v })} label="开机时启动">
          在后台持续统计，窗口不会弹出；可从{isMac ? "菜单栏" : "托盘"}图标打开。
        </Toggle>
        {isMac && (
          <Toggle checked={form.traySpeed} onChange={(v) => set({ traySpeed: v })} label="在菜单栏显示下载速度" />
        )}
      </Card>

      <Card title="多设备同步">
        <Toggle checked={form.tailscale} onChange={(v) => set({ tailscale: v })} label="通过 Tailscale 自动发现并同步（推荐）">
          无需配置：同一 tailnet 中运行 Clash Traffic 的电脑会自动互相发现，每分钟拉取对方的流量统计并合并显示。
          数据只在你自己的设备之间直接传输（不经第三方），他人共享进 tailnet 的设备无法读取；访问记录不会共享。
        </Toggle>
        <p class="muted small sub-head">其他同步方式（可选）：设备不同时在线时，可以再通过共享文件夹或 WebDAV 交换数据。</p>
        <Field label="同步方式">
          <div class="segmented">
            {(
              [
                ["off", "关闭"],
                ["folder", "共享文件夹"],
                ["webdav", "WebDAV"],
              ] as const
            ).map(([k, label]) => (
              <button type="button" key={k} class={form.syncKind === k ? "on" : ""} onClick={() => set({ syncKind: k })}>
                {label}
              </button>
            ))}
          </div>
        </Field>
        {form.syncKind === "folder" && (
          <Field label="文件夹" hint="选择 iCloud 云盘、OneDrive、Dropbox、Syncthing 或网络共享中的文件夹，将在其中创建 ClashTraffic 子文件夹">
            <div class="row">
              <input type="text" value={form.syncFolder} placeholder="未选择" onInput={(e) => set({ syncFolder: e.currentTarget.value })} />
              <button type="button" class="ghost" onClick={chooseFolder}>
                选择…
              </button>
            </div>
          </Field>
        )}
        {form.syncKind === "webdav" && (
          <>
            <Field label="服务器地址" hint="坚果云：https://dav.jianguoyun.com/dav/ ；也支持 Nextcloud、群晖、Alist 等">
              <input type="url" value={form.webdavUrl} onInput={(e) => set({ webdavUrl: e.currentTarget.value })} />
            </Field>
            <Field label="用户名">
              <input type="text" value={form.webdavUser} autoComplete="off" onInput={(e) => set({ webdavUser: e.currentTarget.value })} />
            </Field>
            <Field label="密码" hint="坚果云需在「账户信息 → 安全选项」中生成应用密码">
              <input
                type="password"
                value={password ?? ""}
                placeholder={saved.hasWebdavPassword ? "已保存（留空不修改）" : ""}
                autoComplete="new-password"
                onInput={(e) => setPassword(e.currentTarget.value === "" && !saved.hasWebdavPassword ? null : e.currentTarget.value)}
              />
            </Field>
            <Field label="远端目录">
              <input type="text" value={form.webdavDir} onInput={(e) => set({ webdavDir: e.currentTarget.value })} />
            </Field>
          </>
        )}
        {form.syncKind !== "off" && (
          <>
            <Field label="自动同步间隔">
              <select value={String(form.syncMinutes)} onChange={(e) => set({ syncMinutes: Number(e.currentTarget.value) })}>
                {[1, 5, 10, 30, 60].map((m) => (
                  <option key={m} value={m}>
                    每 {m} 分钟
                  </option>
                ))}
              </select>
            </Field>
            <div class="row">
              <button type="button" class="ghost" disabled={busy === "sync"} onClick={testSync}>
                {busy === "sync" ? "测试中…" : "测试同步"}
              </button>
              {syncTest && <span class={syncTest.ok ? "ok-text" : "error-text"}>{syncTest.ok ? `✓ ${syncTest.msg}` : `✕ ${syncTest.msg}`}</span>}
            </div>
          </>
        )}
      </Card>

      <Card title="数据">
        <Field label="访问记录保留" hint="每个连接的记录只保存在本机；按小时的流量统计不受影响，永久保留">
          <select value={String(form.sessionDays)} onChange={(e) => set({ sessionDays: Number(e.currentTarget.value) })}>
            {[
              [7, "7 天"],
              [30, "30 天"],
              [90, "90 天"],
              [365, "1 年"],
              [0, "永久"],
            ].map(([v, label]) => (
              <option key={v} value={v}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <dl class="kv">
          <dt>数据目录</dt>
          <dd>
            <code>{about?.dataDir}</code>{" "}
            <button type="button" class="link" onClick={() => Preferences.openDataFolder().catch((err) => notify(errorText(err), "error"))}>
              打开
            </button>
          </dd>
          <dt>设备 ID</dt>
          <dd>
            <code>{about?.deviceId}</code>
          </dd>
          <dt>版本</dt>
          <dd>
            {about?.version || "开发版"} · {about?.platform}
          </dd>
        </dl>
      </Card>

      <div class={`savebar${dirty ? " show" : ""}`}>
        <span>有未保存的修改</span>
        <button type="button" class="ghost" onClick={() => (setForm(saved), setPassword(null))}>
          还原
        </button>
        <button type="button" class="primary" disabled={busy === "save"} onClick={save}>
          {busy === "save" ? "保存中…" : "保存"}
        </button>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ComponentChildren }) {
  return (
    <div class="field" role="group" aria-label={label}>
      <span class="field-label">{label}</span>
      <span class="field-body">
        {children}
        {hint && <span class="hint">{hint}</span>}
      </span>
    </div>
  );
}

function Toggle({ checked, onChange, label, children }: { checked: boolean; onChange: (v: boolean) => void; label: string; children?: ComponentChildren }) {
  return (
    <div class="toggle-row">
      <label class="switch big">
        <input type="checkbox" checked={checked} onChange={(e) => onChange(e.currentTarget.checked)} />
        <span>{label}</span>
      </label>
      {children && <p class="hint">{children}</p>}
    </div>
  );
}
