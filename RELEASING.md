# 发布与打包

本项目使用无 `v` 前缀的 tag，例如 `0.2.0`。修改 `mygo.config.ts` 的版本号，并在 `CHANGELOG.md` 增加相同版本的说明后再打 tag。应用标识为 `dev.easoncan.clashtraffic`。

MyGo 0.4.8 按应用名称保存用户数据，仍使用原来的 `Clash Traffic` 目录，因此更换标识不会丢失统计、设置、更新偏好或窗口状态；原有数据目录锁也会阻止新旧版本同时写入。安装时先退出旧版，并用新版替换同名应用。macOS 开机启动注册与应用标识有关，需要在新版设置中重新开启；Windows 从上游切换时建议先卸载旧程序，保留用户数据，以免系统中留下两个卸载条目。

## 签名密钥

`updates.publicKey` 是本 fork 的 Ed25519 更新公钥，对应私钥保存在仓库 Actions secret `MYGO_UPDATER_PRIVATE_KEY`，不能提交到 Git。

维护者本机的私钥位于 `~/Library/Application Support/mygo/update-keys/Easoncan-clash-traffic/mygo-update.key`，权限应为 `0600`。请将其安全备份；后续版本必须沿用同一密钥。更换电脑时可以从安全备份恢复，并重新设置 Actions secret：

```sh
gh secret set MYGO_UPDATER_PRIVATE_KEY --repo Easoncan/clash-traffic < /path/to/mygo-update.key
```

自动更新签名与 Apple Developer ID / Windows Authenticode 是不同的签名。本项目目前没有配置后两者，因此 macOS 仅 ad hoc 签名，不做 Apple 公证；Windows 安装程序也不具有发布者证书。不要将更新包签名描述为已通过平台认证。

上游 0.1.4 的更新源和公钥不能通过本 fork 的服务端更换。用户需要手动安装一次本 fork 的版本，之后才会从本项目接收签名更新。

## 本地验证

```sh
bun install --frozen-lockfile
bun run generate
git diff --exit-code -- src/mygo.ts
CGO_ENABLED=0 go test ./...
go test -race ./...
go run github.com/egoist/mygo/cmd/mygo vet .
bun run test:web
bun run build:web
```

普通 `bun run build` 仍可用于本地调试，但缺少私钥时 MyGo 会跳过更新清单。发布应使用下列脚本；它要求提供密钥，且在缺包、版本不符或签名验证失败时停止：

```sh
export MYGO_UPDATER_PRIVATE_KEY="$(cat '/path/to/mygo-update.key')"
bun run build:release darwin/universal
unset MYGO_UPDATER_PRIVATE_KEY
```

产物位于 `build/release-darwin-universal/assets/`。macOS DMG 必须在 macOS 上构建；Windows 安装程序需要 NSIS；Linux 产出两种架构的 `.deb` 和 `.tar.gz`。

MyGo 0.4.8 内置的 GitHub 上传使用 `v` 前缀。不要使用 `mygo build -upload` 发布本项目：`scripts/release.ts` 会将本次更新包和增量包的下载地址修正为实际 tag，保留历史包地址，校验文件大小和 Ed25519 签名，并给通用 macOS 包生成两种 CPU 的清单。

## GitHub Actions

`.github/workflows/release.yml` 在 main 和 PR 上运行检查。推送 `0.2.0` 形式的 tag 后，检查通过才开始并行打包：

- macOS 13+：Apple 芯片 / Intel 通用 DMG、签名更新包和三份兼容清单。
- Windows：x64 / ARM64 安装程序、签名更新包和清单。
- Linux：x64 / ARM64 Debian 包、归档包、安装脚本和清单。

全部成功后上传产物和 `SHA256SUMS` 到同名的**草稿 Release**，不自动发布。检查安装和升级后，可在 GitHub 上发布草稿并设为 latest，应用才会收到更新。失败或中断的任务不会发布半成品，重跑也不会覆盖已经公开的 Release。

提交推送示例（先完成本地检查）：

```sh
git tag -a 0.2.0 -m 'Release 0.2.0'
git push --atomic origin main refs/tags/0.2.0
```
