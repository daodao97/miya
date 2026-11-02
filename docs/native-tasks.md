# 会话任务状态栏

桌面和手机的输入框上方统一显示 Codex Goal 与 Claude Code Loop。状态来自各自的原生协议，通过 Miya 后台服务同步；切换项目或关闭窗口不会停止后台正在执行的任务。

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

状态栏区分“等待执行”和“运行中”。原生协议没有提供后续准确时间时，保留周期信息，不推算一个时间冒充原生调度结果。点击“停止循环”会要求 Claude 取消当前会话的全部定时任务并结束动态循环；只有收到实际取消结果后，任务才显示为停止或完成。

模型和权限变更通过原生会话控制生效。stream-json 没有运行中修改推理强度的接口，需先停止循环，再调整强度并重建循环；Miya 会保留草稿并显示提示。

Loop 仍遵循 Claude 的会话生命周期和过期规则。服务退出或进程异常后，内存任务结束；原生持久任务能否恢复由 Claude Code 管理。Miya 不把 Loop 转换为 Automations。

## 验证

常规 `go test ./...` 包含原生 RPC、调度跨轮存活、普通消息复用、取消结果、后台项目路由、手机任务元数据增量和桌面/手机布局测试。可用 `MIYA_TASK_SNAPSHOT=/tmp/miya-tasks go test -run TestTaskBarDesktopAndMobile` 导出渲染快照。
