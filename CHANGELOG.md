# 更新日志

每个版本的说明也会显示在应用的更新窗口中。

## 0.2.0

- Closing the last macOS window removes the Dock icon while counting continues in the menu bar; reopening restores it.
  macOS 关闭最后一个窗口后隐藏 Dock 图标，菜单栏继续统计；重新打开窗口时恢复图标。
- Exclude mihomo's internal dialer-proxy hops from app/subscription statistics, live connections and access records, avoiding duplicate traffic with chained proxies and custom listeners.
  排除 mihomo 链式代理的内部中转连接，避免自定义监听端口和代理分组产生重复流量统计；实时连接和访问记录同步处理，升级时丢弃旧版可能重复计算的对账差额，保留历史记录。
- On macOS, missing process metadata can be supplemented by a uniquely matching local TCP/UDP socket. Later identification applies to subsequent traffic; destinations alone never determine the app, and past unknown traffic is kept as recorded.
  macOS 可通过唯一匹配的本机 TCP/UDP 套接字补充进程信息，后续识别成功会用于新增流量；不按域名猜测应用，也不改写历史未识别记录。
- Settings shows storage usage and offers a second confirmation before clearing traffic and access records. Settings are retained, counting continues, and active connections do not bring back pre-clear traffic.
  设置页显示存储占用，并提供二次确认后清除统计和访问记录；保留设置、继续统计，清除前的连接流量不会重新计入，页面筛选和曲线同步重置。
- Separate connection testing from process identification. Automatic maintenance defaults on; checks read the running core's configuration and existing connection results. Changes are read back to confirm they took effect, with core identification and local supplementation shown separately.
  连接测试和进程识别之间增加分隔；默认开启自动维护，通过读取运行中内核的配置及连接结果检查状态，修改后回读确认生效，分别展示内核识别和本机补充数量。
- Updates now use Easoncan/clash-traffic and the fork's signing key. A repository without releases, or a missing manifest for the current version, reports up to date; a newer incomplete release or a network error remains an error. Universal macOS manifests serve both CPUs.
  更新源改为 Easoncan/clash-traffic，使用本项目独立签名密钥；尚未发布 Release 或当前版本缺少清单时显示已是最新，存在更高版本但缺包或网络失败时仍提示错误，兼容 macOS 通用更新包。
- The application identifier is now dev.easoncan.clashtraffic. Existing data and settings keep using the same directory; quit the old app before replacement and re-enable launch at login in the new macOS app if needed.
  应用标识改为 dev.easoncan.clashtraffic，继续使用原数据和设置目录；替换前请退出旧版，macOS 如需开机启动，请在新版中重新开启。
- Release packaging validates the embedded version against bare tags such as 0.2.0, verifies signed archives, corrects download URLs and builds macOS, Windows and Linux packages into a draft release only after all platforms pass. Existing upstream installations need one manual installation of this fork to switch update sources and signing keys.
  发布流程校验内置版本与 0.2.0 等无前缀 tag 一致，验证更新包签名并修正下载地址，macOS、Windows、Linux 全部打包通过后生成草稿 Release。上游旧版需手动安装一次本项目版本，以切换更新源和公钥。

## 0.1.4

- Exporting CSV from Apps or Hosts keeps the search: the file holds the lines the page shows.
  在「应用」「访问地址」页导出 CSV 时会应用搜索词，导出内容与页面所见一致。
- With a device picked, Subscriptions splits each subscription's traffic by that device only.
  在「订阅」页选择设备后，各订阅下的设备分项只显示所选设备。
- Pages no longer dim every few seconds as they refresh; chart tooltips show dates in the app's language.
  页面定时刷新时不再每隔几秒变暗一次；图表提示中的日期按所选语言显示。
- On Windows, Clash Verge's core is found by its running pipe too, as in service mode; Settings shows the order the app searches in.
  Windows 上也会查找运行中内核的管道（例如服务模式）；设置页中的查找顺序说明已更新。
- A settings file that cannot be read is kept as settings.json.unreadable-…, instead of being replaced by the defaults.
  无法读取的设置文件会另存为 settings.json.unreadable-…，不再被默认设置覆盖。
- Files are on disk before they replace the old ones, so a power cut leaves no empty file; queries of long ranges no longer hold up counting.
  文件先写入磁盘再替换旧文件，断电后不会留下空文件；查询较长时间范围时不再阻塞流量统计。
- The macOS app says it needs macOS 13 or later, as its code does; it runs on Apple silicon and Intel alike.
  macOS 版本标明需要 macOS 13 及以上（与程序实际要求一致），Apple 芯片和 Intel 均可运行。

## 0.1.3

- **Languages**: English, 简体中文, 繁體中文, 日本語, 한국어 and Русский. The app follows the system; choose another in Settings → This computer → Language. The menus, the tray and the messages follow too.
  **多语言**：支持简体中文、繁體中文、English、日本語、한국어、Русский，默认跟随系统，可在「设置 → 本机 → 语言」中切换；菜单、菜单栏和提示信息同步切换。
- Names the app gives unknown traffic, such as "Unidentified process", show in your language and can be searched in it.
  “未识别进程”等名称按所选语言显示，也能用该语言搜索。
- Card headers wrap their tools under the title when space runs short; the macOS menu bar menus are translated.
  卡片标题栏空间不够时工具按钮自动换行；macOS 菜单栏菜单已翻译。

## 0.1.2

- **自动更新**：每天检查一次新版本，显示更新说明，一键下载安装并重启；也可以在「设置 → 软件更新」中改为自动安装，或从菜单栏图标、应用菜单「检查更新…」手动检查。更新包经过签名校验，通常只需下载有变化的部分。
- 开启 Clash Verge 系统代理时，检查和下载更新会经由 Clash 的代理端口访问 GitHub。
- 菜单栏图标重新绘制：更宽的猫脸、更粗的箭头，小尺寸下更清晰。
- macOS 窗口左上角：红绿灯按钮下移，与 Logo、页面标题之间的间距更协调；全屏时 Logo 和标题不再贴着顶部。

## 0.1.1

- 新图标：呼应 Clash Verge 的猫，眼睛是上传、下载两个箭头。
- 修复实时页“最近 5 分钟”速度曲线只显示填充、不显示线条。

## 0.1.0

- 首个版本：按应用、访问地址、节点、代理/直连、订阅、设备统计 Clash Verge 的流量；访问记录与实时连接；通过 Tailscale 自动合并多台电脑的统计，也可用共享文件夹或 WebDAV 同步。
