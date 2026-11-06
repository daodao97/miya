# Chat scope

| 子包 | 责任 |
| --- | --- |
| `model` | 会话、消息、引用、计划、任务与中立活动元数据 |
| `composer` | 输入解析、补全与引用插入 |
| `history` | 会话列表的磁盘缓存 |
| `presentation` | 消息分组、正文、活动标题、图标、格式、交接摘要和工具投影 |
| `text` | 共用文本、路径、标题和旧转录格式的兼容读取 |
| `subagent` | 已规范化子任务的状态、汇总与消息合并 |
| `usage` | 对话 Token 计数，与 Agent 订阅配额分开 |
| `view` | 桌面和手机共用的会话视图，通过 Host 和回调接入界面 |

共享模型和展示层不导入具体 Agent 实现。Codex 的原生 Item、历史、计划、目标和子任务转换位于 `agent/codex/projection`，Claude 的原生用量与时序转换位于 `agent/claude/projection`。原生客户端继续拥有各自协议。

`model.Activity` 只提供公共功能实际需要的字段，并在序列化时保留旧 Item 的额外字段，避免旧缓存、旧服务或跨版本窗口丢失数据。`model.MessagePresentation` 是视图合同，新接入可直接提供；旧格式的文本兼容读取保留在共享格式化层，不要求视图导入供应商代码。

`agent/catalog` 描述会话恢复等能力；历史缓存按能力清除仅在连接中有效的状态，不按供应商名称分支。订阅读取器由入口通过 UI Environment 注入，界面只使用公共 quota.Service。
