# Phone scope

`internal/phone` 收拢手机连接及双端访问所需的协议、服务、缓存和代理。界面仍由 `internal/mobile`、`internal/ui` 负责。

| 子包 | 职责 | 原位置 |
| --- | --- | --- |
| `protocol` | 状态、增量、审批消息与项目文件路径规则 | `phoneapi`，以及 `phone.ProjectFileID` |
| `server` | 桌面侧配对、会话同步、文件服务、接收上传 | `phone` |
| `cache` | 手机状态缓存、桌面登记和切换时的草稿状态 | `phonecache` |
| `proxy` | 客户端文件与 Web 代理，两端界面共用 | `phoneproxy` |
| `transfer` | 两端共享的附件限额、图片读取与过期文件清理 | `phone/images.go` 中的共享部分 |

依赖方向：`server` 使用 `protocol`、`transfer`；`cache`、`proxy` 使用 `protocol`。手机界面使用客户端子包，不能依赖 `server`。这些子包都不依赖控制器或界面实现，配对服务通过 `Host` 接入控制器。

项目会话快照和列表摘要位于 `model.ProjectSnapshot`、`model.ListThread`，桌面搜索不再依赖手机协议。协议继续使用相同 JSON 字段、项目文件 ID 算法及磁盘路径，现有配对与缓存可继续使用。`protocol.LegacyResponse` 保留旧后台审批回复的兼容编码。

底层连接、加密与传输通道仍由 `internal/remote` 提供；该层同时服务桌面远程工作区和终端连接，不属于手机专用实现。

本次迁移通过全量 race 测试（1248 项通过、26 项跳过）、`go vet ./...`，以及 macOS、iOS 模拟器、Linux 和 Windows 构建。协议兼容性测试覆盖稳定文件 ID 和项目状态缓存格式；集成测试约束包间依赖方向。
