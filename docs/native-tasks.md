# 会话任务状态栏

桌面和手机的输入框上方显示 Codex Goal、Claude Code Loop 和 Miya 跟进计时器。Goal 和 Loop 的状态来自原生协议，跟进计时器由 Miya 后台保存。切换项目或关闭窗口后，后台仍可执行任务。

会话工作期间，状态栏还会列出仍在运行的子 Agent（Claude Code 的 Task/Agent、Codex 的协作 Agent 等，统一来自消息上的 SubAgent），显示名称、步数和最新进度，不随消息列表滚动消失。子 Agent 结束后对应行随即移除；主 Agent 停止工作后不再列出，避免只有启动快照、没有终态回报的子 Agent 长期残留。完整记录仍在消息列表里。

从 `/` 菜单选中可执行命令后，命令名在输入框内显示为独立标签，后面可填写参数，并显示参数提示。选择不会直接执行，填写后发送才执行；`/model`、`/permissions` 等界面入口仍直接打开对应界面。标签支持整块删除、复制、剪切、粘贴和撤销，与会话引用可同时使用。复制和发送保留原始 `/命令 参数` 文本；手机上的长命令名会截短显示，完整名称仍保留。

## Codex

```text
/goal 修复 CI，并确认所有测试通过
/goal --budget 50000 修复 CI，并确认所有测试通过
/goal
/goal pause
/goal resume
/goal clear
```

Miya 调用 app-server 的 `thread/goal/get`、`set`、`clear`，以 `origin: user` 标记用户操作，并订阅 `thread/goal/updated` 和 `cleared`。目标执行与自动续轮由 Codex 管理。打开已有对话时重新查询目标，旧版 CLI 不支持接口时会显示操作错误。

状态栏显示目标、运行/等待/暂停/阻塞/完成状态、token 使用量及可选预算，并提供暂停、继续和清除按钮。暂停目标控制后续自动执行，当前回合的执行状态仍由 Codex 决定。

## Claude Code

```text
/loop 5m 检查部署是否完成
/loop 检查 CI 和审阅意见
/loop
```

命令直接交给 Claude Code。Miya 从成功的 `CronCreate`、`CronList`、`CronDelete`、`ScheduleWakeup` 工具结果读取任务 ID、状态及原生下次执行时间，包含 Claude 的调度偏移。有任务等待时保留同一个 stream-json 进程；自动触发的新回合与普通消息继续使用这个会话。

状态栏区分“等待执行”和“运行中”。原生协议没有提供后续准确时间时，保留周期信息，不推算一个时间冒充原生调度结果。点击“停止循环”会要求 Claude 取消当前会话的全部定时任务并结束动态循环；只有收到实际取消结果后，任务才显示为停止或完成。循环结束（完成、停止或失败）后与 Goal 一样提供清除按钮；清除只移除这条记录，同一 Claude 进程后续上报的已结束定时任务不会再加回，新建的循环照常出现。

模型和权限变更通过原生会话控制生效。stream-json 没有运行中修改推理强度的接口，需先停止循环，再调整强度并重建循环；Miya 会保留草稿并显示提示。

Loop 仍遵循 Claude 的会话生命周期和过期规则。服务退出或进程异常后，内存任务结束；原生持久任务能否恢复由 Claude Code 管理。Miya 不把 Loop 转换为 Automations。

## Miya 跟进计时器

Agent 可以通过 MCP 工具 `follow_up` 给当前会话登记一次跟进。`at` 是带时区的 RFC 3339 时间，`note` 是一句最多 400 字符的说明。每个会话最多有三个待处理跟进，最远可以设到 30 天后。

| | Goal / Loop | `follow_up` |
| --- | --- | --- |
| 谁发起 | 用户用 `/goal`、`/loop` | 对话里的 Agent 自己 |
| 做什么 | 持续推进一个目标，或反复执行同一段提示 | 一次性：到期时把一句说明作为新消息交回同一个对话 |
| 谁保存 | CLI 自己；Miya 只读取和显示 | Miya 后台，落盘 |
| 服务或进程退出后 | Loop 的内存任务结束，能否恢复由 Claude Code 决定 | 未发送的跟进由后台恢复，在到期且会话空闲后投递 |
| 适用的 Agent | Goal 只有 Codex，Loop 只有 Claude Code | 所有 Agent |

- 用户要的反复检查（「每 5 分钟看一次部署」）用 Loop。Agent 自己说「稍后再看」，而这件事可能要跨过进程退出或后台重启，用 `follow_up`。
- Claude Code 在 Loop 里用 `ScheduleWakeup` 给自己排下一次，这仍然是 Loop 的一部分，Miya 不把它改写成 `follow_up`。
- 待处理的跟进列在任务栏里，显示到期时间和说明。用户可以逐项取消。停止、归档或删除会话时，后台取消该会话的跟进。关闭窗口后，待投递的跟进会让后台保持运行。
- Miya 和委派完成通知共用投递机制。已确认发送的跟进从任务栏消失。如果后台重启后无法确认 Agent 是否已收到跟进，任务栏会显示「需要处理」，用户可以关闭这条记录。
- 要长期按时间或条件反复运行、并且不依赖某个对话还开着的事，是 Automations（`docs/automations.md`），不是这两者。

## 验证

常规 `go test ./...` 包含原生 RPC、调度跨轮存活、普通消息复用、取消结果、后台项目路由、手机任务元数据增量和桌面/手机布局测试。可用 `MIYA_TASK_SNAPSHOT=/tmp/miya-tasks go test ./tests/integration -run TestTaskBarDesktopAndMobile` 导出渲染快照。
