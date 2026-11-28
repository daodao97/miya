# Miya 项目约定

## 构建、安装与后台服务升级

用户要求安装时，按 [安装与后台服务升级](docs/install-and-upgrade.md) 完成构建、验签、备份替换、重开窗口和状态核验。安装已经得到授权后，继续执行，不要再停在操作建议或重复确认。

- 窗口、`-server` 后台服务、`-mcp` 工具进程分别运行。更新应用文件不等于运行中的后台已经更新。
- 重开窗口后，由 `connectService` 判断后台版本并安排切换。后台忙碌时保留它，让其空闲后退出，窗口自动重连并启动新版。
- 不要用 `pkill Miya`、`killall Miya` 或强杀后台来使更新生效；这会中断其他项目、手机端或当前执行安装的对话。
- 验收分别报告已安装的二进制、窗口是否重开、后台是否已切换。后台仍忙碌时明确说明延期生效，不要等待当前对话结束才返回，也不要声称前后台都已更新。

## 默认界面与构建

NSGo 是唯一界面入口，使用 `go build ./...`、`go test ./...`，不加迁移构建标签。打包走 `scripts/build-macos.sh` / `build-ios.sh` / `build-android.sh`，配置在 `nsgo.yaml`。开发依赖保留 `../nsgo-miya` replacement；CI 需先发布 fork，见 [NSGo 打包](docs/nsgo-packaging.md)。设备验收和上游 PR 仍待完成，见 [迁移记录](docs/nsgo-migration.md)。使用共享 Go cache；磁盘受限时及时删除已验证的 build 产物，不跑全仓库 race。
