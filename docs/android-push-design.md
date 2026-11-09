# Android 通知：经配对连接接收

状态：已实施，2026-11-09。iOS 的 APNs 链路不变，见 [通知](notifications.md)。

## 方案

Android 不接任何第三方推送服务（FCM、UnifiedPush、厂商通道都不用）。手机本来就通过 tailcat 隧道（经公共 DERP relay 的端到端加密连接）连着桌面；Android 允许前台服务保活进程，所以 Miya 在后台继续保持这条连接，桌面直接把通知写进去。

- 不需要 Google/阿里云/厂商账号，不需要运营服务器，内容不经第三方。
- 代价：通知栏常驻一条低优先级通知（「正在接收电脑通知」），以及一条空闲长连接的耗电。
- 进程被系统或用户结束后收不到通知，也不自动重启；不做兜底转发，不做断线补发。手机重新打开 Miya 时照常读取未读状态。

## mygo（只提供通用能力）

- `Power.KeepRunning(reason)`：上游已有的接口。Android 后端实现为前台服务 `MygoKeepRunningService`（类型 `specialUse`，`START_NOT_STICKY`），按引用计数启停，常驻通知显示最近的 reason。Android 12 起只能在前台启动前台服务，应用应在前台时调用。
- 本地通知：`mygo.NewNotification` 在 Android 上点击直接打开 Activity（不再经广播跳板，Android 12 起跳板无法把应用带回前台），intent 带本机随机密钥，伪造的点击会被忽略。
- 推送通道注册接口：`RegisterPushNotifications`、`OnPushSubscription`（`Service`、`Token`、`Data`）保留为通用接口。应用实现 `org.mygo.MygoPushChannel`，在 Manifest meta-data `org.mygo.push.channels` 中按优先级列出类名；mygo 自己不带任何通道。

## Miya

- 手机：Android 获得通知权限后以 `Registration{Live: true}` 注册（无 token），不调用 `RegisterPushNotifications`。开着手机通知且已配对电脑时，在前台持有 `KeepRunning`；退到后台不断开连接。收到 `notice` 帧后经 `ShouldPresentNotification` 判断（正在看的会话不弹），用 `mygo.NewNotification` 显示，点击走 `OpenNotification`；角标清零时收起已显示的通知。
- 桌面：`onPush` 收到 Live 注册后 `Hub.AttachLive(id, ctx, send)`，会话结束（ctx 结束）时自动摘除。`Hub` 的队列遇到 Live 注册时，把 `notificationFor` 构造的同一条消息编成 `notification.LiveMessage` 写成 `notice` 帧；手机不在线就丢弃，不重试。未读、角标、去重、桌面在场抑制与 iOS 共用。

## 验收（API 36 模拟器，隔离桌面实例）

- 前台服务启动，`isForeground=true`，类型 `specialUse`；退到后台 60 秒后进程仍在、`curProcState=4`，连接未断。
- 桌面注册为 `live: true`、无 token。
- 后台时完成一轮任务，手机显示「Miya · Completed / ok」；点击后 Miya 回到前台、打开对应会话，桌面清除该会话的未读。
- 单元测试：Live 注册校验、多台无 token 设备互不替换、投递、已读后角标清零、会话结束后不再投递。

未验收：真机厂商 ROM 的后台清理、Doze 长时间待机、电量消耗、多台电脑同时配对。注意：手机快照只包含桌面项目列表中的项目，不在列表中的项目的会话，通知点击后无法打开（iOS 相同）。
