# 迁移到 NSGo

分支 `nsgo` 把 Miya 的界面从 MyGo 迁到 [NSGo](../../nsgo)（模块 `github.com/nsgo-dev/nsgo`，开发期 `replace` 到 `../nsgo-miya`，它是 `../nsgo` 的独立 worktree）。控制器、后台服务、Agent 适配、自动化和手机协议不随界面框架变化，迁移只替换视图层和平台服务。

## 为什么不能只改 import

MyGo 是即时模式：`ui.Row(c).Children(func() { ... })` 每帧重建，视图直接读控制器的 getter。NSGo 是保留模式：组件只运行一次，变化通过信号驱动的绑定（`XFunc` 字段、`Show`/`Each`/`Dynamic` 区域）传到原生视图。两者的组件写法、状态归属和布局 API 都不同，每个视图需要重写。

规模（迁移开始时）：

| 包 | 非测试行数 | 说明 |
| --- | --- | --- |
| `internal/ui` | 9.2k | 桌面窗口、侧栏、设置、自动化、托盘 |
| `internal/mobile` | 10.6k | 手机端 |
| `internal/desktop` | 2.8k | 远程桌面 |
| `internal/widgets` | 1.6k | 通用控件、主题、图标 |
| `internal/chat/view` | 1.2k | 会话视图（桌面和手机共用） |
| `internal/voiceinput` | 1.1k | 语音输入 |
| `tests/integration` | 13.8k | 依赖 MyGo 测试器的界面测试 |

## 依赖的 NSGo 检出

Miya 不直接用 `../nsgo`：那里可能正在开发 NSGo 本身（补 Miya 需要的平台能力，之后向上游提 PR），半成品会让 Miya 无法构建。`../nsgo-miya` 是 `git -C ../nsgo worktree add` 建的独立检出，固定在已验证的提交上（当前为 `platform-services` 分支的 `c246c3fc`）；NSGo 的改动完成并通过测试后，再把它前移到对应分支或提交。

## 参考实现

[Agio](https://github.com/oneo-me/nsgo-agio)（相邻检出 `../nsgo-agio`，只读）是 NSGo 作者用 NSGo 写的编码 Agent 外壳，`chatview/conversation.go` 的会话、Markdown（`ui.TextView`）、悬浮输入框与模型选择是 Miya 对应视图的首选参考。

## 并存方式

迁移期间两套界面并存，每一步都能构建和发布：

- 默认构建仍是 MyGo 版本（`main.go`，`//go:build !nsgo || ios || android`）。
- `go build -tags nsgo .` 在桌面平台使用 `main_nsgo.go`，入口 `internal/nsui.DesktopMain`。手机端在迁移完成前继续走 MyGo。
- `internal/nsui` 不带构建标签，`go build ./...` 和 `go test ./...` 始终编译并测试它，避免新界面腐烂。
- 包边界测试把 `internal/nsui` 视同其他视图包：不得导入控制器、Agent 实现或旧界面包。

## 桥接层

控制器按即时模式编写：状态是 getter，setter 调用 `uiruntime.Invalidate()` 请求新帧。`internal/nsui/bridge.go` 让控制器原样驱动 NSGo：

- `bridge` 实现 `uiruntime.Window`。`Update(f)` 经 `Scope.Post` 在引擎协程执行 `f`；`Invalidate()` 可从任意协程调用，同一次唤醒内的多次调用合并为一次修订号递增。
- `Live(getter)` 把 getter 包成绑定：先读修订号信号再读 getter。控制器每次失效后绑定重算，NSGo 只发出实际变化的部分。需要更细粒度时再用 memo 包装。
- 通知走 NSGo 的 `ShowNotification` / `OnNotificationPress`。空闲时间暂不提供（`uiruntime.IdleTime` 为 nil，视为用户在场）。
- `IsFocused` 暂以窗口可见近似，等 NSGo 提供 key window 状态后替换。

## 阶段

| 阶段 | 内容 | 完成标准 |
| --- | --- | --- |
| 0 · 基础 | 依赖、构建标签入口、桥接层、会话列表骨架 | 两种构建通过；桥接测试（含 `-race`）通过 ✅ |
| 1 · 主题与控件 | `internal/nswidgets`：Miya 明暗色板作为 `core.Theme`（自有角色放 `Palette.Colors`），`ColorsOf` 颜色访问器、图标与通用控件；Tabler 图标移到框架无关的 `internal/iconassets` 供新旧两套共用。设置页布局与菜单随第 3 阶段迁移 | 主题、外观切换、图标解析与控件测试通过 ✅（设置页、菜单待做） |
| 2 · 会话视图 | `internal/chat/view` 的消息列表、Markdown、工具详情、图片、引用和富文本输入（NSGo `ui/chat`、`base/richeditor`） | 桌面能完整对话。进度：`internal/nschat` 的消息列表已完成——分组复用 `chat/presentation`，助手回复用 `base/textview` 流式 Markdown（流式时行不重建），工具活动用 `chat.ActivityRow`，详情按计划、diff、文件、Markdown、等宽输出呈现，错误卡片可重试；外层为 `chat.FloatingConversation`，回合状态、后台任务与 `chat.Composer`（草稿、发送/引导、停止）悬浮在末尾。用户消息显示图片缩略图与附件标签，完成的回复下有复制与交接操作（交接接线待第 3 阶段，逻辑在旧 `internal/ui.App`）。待做：会话引用（@ 提及）、子代理列表、粘贴与拖入附件 |
| 3 · 桌面外壳 | 侧栏、工作区切换、文件面板、命令面板、设置、自动化、托盘、多窗口 | 进度：`ui.SidebarShell`（内嵌卡片、可折叠拖宽）、新会话按钮、会话按运行中/今天/昨天/7 天/30 天/更早分组的侧栏、标题栏已完成。macOS 菜单栏（应用、文件、编辑、显示、窗口）与快捷键 `⌘N` `⌘,` `⌘B` `⌘.` `Control+Tab` 已完成；设置页由 Codex 在 `nsgo-settings` 分支迁移中。与 MyGo 版功能对齐，`internal/ui` 的界面测试迁到 nsgotest |
| 4 · 手机端 | `internal/mobile`、远程桌面、语音输入，接 NSGo 的 iOS/Android 运行时 | 真机验证 |
| 5 · 收尾 | 删除 MyGo 依赖、旧界面包和构建标签；构建脚本与 CI 改用 `go tool nsgo` | `go list -deps` 不含 `egoist/mygo` |

## 需要 Miya 自己补的平台能力

已在本地 NSGo 补齐、待向上游提 PR（`../nsgo` 的 `platform-services` 分支，macOS 实现，其他平台返回 `ErrPlatformUnsupported`）：Keychain（`Scope.StoreSecret` / `ReadSecret` / `DeleteSecret`、`ErrSecretNotFound`）、系统空闲时间（`SystemIdleTime`，`nsui` 每 15 秒采样给 `uiruntime.IdleTime`）、网络路径（`ReadNetworkPath` / `ObserveNetworkPath`）、窗口焦点与最小化（`Window.IsFocused` / `IsMinimized`，桥接层镜像为原子值供控制器跨协程读取）。真实的网络切换与窗口焦点切换尚未在真机验证。

仍需补的：生物识别、扫码、窗口内 WebView 的部分选项。

非界面包对 MyGo 的残留依赖（第 5 阶段前处理）：

- `locales`、`internal/phone/server`：`mygo/i18n` 与 `mygo/locales/zh-Hans`。
- `internal/desktop`：`mygo/plugins/permissionflow`。

## 验证

```sh
go build ./... && go build -tags nsgo .
go test -race ./internal/nsui
go test ./tests/integration -run 'Boundar|TestViewsDoNotKnowAgents'
```

实际窗口验证按[独立验证流程](install-and-upgrade.md#独立验证不覆盖已安装应用)进行，不覆盖已安装应用、不切换生产后台。
