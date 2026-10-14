# Clash Traffic

统计 Clash Verge（mihomo 内核）代理流量的跨平台桌面小工具，基于 [MyGo](https://mygo.egoist.dev/)（Go 后端 + 系统 WebView 前端），支持 macOS、Windows、Linux。

- **按应用统计**：每个软件的上传 / 下载 / 连接数，Chrome Helper 等子进程归到所属应用
- **按访问地址统计**：每个域名 / IP 的流量，可按主域名合并（`a.b.google.com` → `google.com`）
- **按代理节点统计**：每个节点（含 DIRECT）走了多少流量
- **代理 / 直连分开统计**：所有页面都可切换「全部流量 / 仅代理 / 仅直连」，按机场口径只看代理流量；托盘显示今日代理用量
- **访问记录**：本机每个连接的开始时间、时长、应用、目标地址与 IP、节点链、命中规则、流量，可搜索、可按应用 / 地址 / 节点筛选
- **交叉筛选**：点一个应用看它访问了哪些地址；点一个地址看哪些应用访问了它
- **实时连接**：当前速度曲线、活动连接列表，可断开单个连接
- **多设备合并统计（Tailscale 自动发现，无需配置）**：同一 tailnet 中运行本应用的电脑自动互相发现、每分钟同步，合并查看或按设备筛选，可看到各设备在线状态与实时速度；也可另选共享文件夹 / WebDAV
- **按订阅统计**：代理流量记到 Clash Verge 当时启用的订阅（或节点所属的 proxy-provider），直连不计入；订阅页同时显示机场返回的已用 / 总量 / 到期时间，以及各设备经该订阅的流量
- 托盘 / 菜单栏常驻、开机启动、导出 CSV、深色模式

## 使用

1. 安装并运行 Clash Verge（Rev）。
2. 打开 Clash Traffic，它会自动找到 Clash 的控制接口：
   运行中内核的 `-ext-ctl-unix` 参数 → Clash Verge 配置文件（`external-controller` / `-unix` / `-pipe`）→ 默认地址
   （`/var/run/clash-verge-service/users/<uid>/verge-mihomo.sock`、`/tmp/verge/verge-mihomo.sock`、`\\.\pipe\verge-mihomo`、`127.0.0.1:9097`）。
   找不到时可在「设置 → Clash 连接」手动填写。
3. **进程识别**：Clash Verge 默认 `find-process-mode: strict`，大部分连接不记录来源进程。
   「自动开启进程识别」（默认开）会把运行中内核的模式设为 `always`，Clash Verge 重载配置后自动再设。
   只对之后新建的连接生效；想永久生效，可在 Clash Verge「全局扩展覆写配置」里加 `find-process-mode: always`。

关闭窗口后应用仍在托盘中继续统计。

### 多设备（Tailscale）

在每台电脑上安装 Tailscale 并登录同一个 tailnet，再安装运行 Clash Traffic 即可，不需要任何配置：

- 每台电脑在自己的 Tailscale 地址上提供自己的统计文件（端口 47321，开发版 47322），只响应本 tailnet 自己的设备：MagicDNS 名称在本 tailnet 域名下的设备（他人共享进来的设备保留对方 tailnet 的域名，会被拒绝，也不会去拉取它们的数据）；
- 每分钟通过 `tailscale status --json` 列出 tailnet 中的电脑（macOS / Windows / Linux），询问哪些在运行本应用，再拉取它们有变化的文件；
- 每台设备只提供自己写的文件，拉取的副本总是对方的最新版本，不会冲突；访问记录不共享；
- 「设备」页显示 tailnet 中每台电脑的状态（运行本应用 / 未运行 / 离线）、实时速度和最近同步时间。

两台设备需要同时在线才能交换数据（不经第三方中转）。如果设备很少同时在线，可以在「设置 → 多设备同步」再选一个共享位置：

- **共享文件夹**：iCloud 云盘、OneDrive、Dropbox、Syncthing、NAS 共享目录等，会在其中创建 `ClashTraffic/` 子目录
- **WebDAV**：坚果云（`https://dav.jianguoyun.com/dav/`，需使用应用密码）、Nextcloud、群晖、Alist 等

每台设备只写入以自己设备 ID 命名的子目录，读取其他设备的目录，不会相互覆盖。默认每 10 分钟同步一次，退出时再同步一次。

### 订阅

走代理的流量记到 Clash Verge 当时启用的配置（`profiles.yaml` 的 `current`）；节点来自 proxy-provider 时记到 provider 名；直连和拦截不计入任何订阅。订阅列表和机场返回的用量（`subscription-userinfo`：已用、总量、到期）随设备信息同步，各设备都能看到。更新前记录的代理流量没有订阅信息，显示为「未知订阅」。

## 统计方式

每秒读取一次 mihomo 的 `GET /connections`，按连接 ID 计算两次快照间的字节增量，记到 `(小时, 应用, 进程, 地址, 节点)` 上。

- 两次快照之间关闭的连接，最后一段字节在快照里看不到。程序用内核的全局累计值校准：差额按刚关闭连接最近的活跃度分摊，
  没有可分摊的连接时记为「短连接（未捕获）」，所以总量与 Clash 一致。
- 应用启动前已存在的连接，只统计启动之后的流量；应用重启时从上次保存的状态继续，不会重复计数。
- 局域网其他设备经 allow-lan 走代理的流量，记为「局域网 <IP>」。

代理 / 直连按连接最终出口判断：`DIRECT` 为直连，`REJECT` 为拦截，其余节点为代理；无法归属到连接的「短连接（未捕获）」流量记为未知，只在「全部流量」中计入。

访问记录在连接关闭时写入（退出应用时，仍未关闭的连接也会写入），字节数是最后一次采样时该连接的累计值，因此和按小时的统计口径不同；在两次采样之间开始又结束的极短连接不会出现在记录中。访问记录只保存在本机、不参与同步，默认保留 30 天（`设置 → 数据` 可改为 7 天到永久）。

数据目录（`设置 → 数据`）：

```
<用户数据目录>/Clash Traffic/
  settings.json            设置（WebDAV 密码也在这里，权限 0600）
  collector-state.json     未关闭连接已计的字节，供重启后续算
  data/<设备ID>/device.json
  data/<设备ID>/2026-10-13.json.gz   按 UTC 日期分文件的小时级计数（参与同步）
  sessions/2026-10-13.jsonl.gz        本机访问记录，按连接结束的 UTC 日期分文件（不同步）
  mesh-state.json                      已从其他设备拉取的文件版本
  .lock                                数据目录锁：新启动的实例会等旧实例退出前的最终保存完成
```

每次保存都会给本机的日文件编上递增的版本号（`gen`）。检查点记录对应的版本号，崩溃后若两者不一致，就不再沿用检查点，宁可少计也不重复计数。通过 Tailscale、共享文件夹、WebDAV 收到的副本也只会替换更旧的版本。

## 开发

需要 Go 1.27+ 和 Bun。

```sh
bun install
bun run dev        # 开发：热重载
bun run build      # 打包本机平台：build/<os>-<arch>/
bun run build -- -platform darwin/universal,windows/amd64,linux/amd64   # 交叉打包
go test ./...      # 测试（CLASH_LIVE=1 时会连接本机运行中的 Clash）
go run ./tools/genicon   # 重新生成 resources/icon.png
```

Windows 安装包需要 NSIS（macOS 上 `brew install makensis`），否则只生成 `.exe`。Linux 需要 WebKitGTK 4.1，托盘需要 `libayatana-appindicator3`。

代码结构：

| 路径 | 内容 |
|---|---|
| `main.go` | 窗口、托盘、菜单、生命周期 |
| `core.go` | 采集循环、同步循环、设置 |
| `services.go` | 暴露给前端的 Go 方法与事件（生成 `src/mygo.ts`） |
| `internal/clash` | mihomo 控制接口客户端（unix socket / 命名管道 / HTTP）与自动探测 |
| `internal/collector` | 连接快照 → 流量增量，差额校准 |
| `internal/store` | 按设备、按天的计数文件与查询聚合 |
| `internal/syncer` | 共享文件夹 / WebDAV 同步 |
| `internal/sessions` | 本机访问记录（逐连接日志） |
| `internal/tailnet` | 读取 Tailscale 状态（本机地址、tailnet 中的设备） |
| `internal/mesh` | 通过 Tailscale 自动发现设备、提供与拉取统计文件 |
| `internal/procname` | 进程路径 → 应用名 |
| `src/` | Preact 前端：概览、应用、访问地址、访问记录、实时连接、订阅、设备、设置 |
