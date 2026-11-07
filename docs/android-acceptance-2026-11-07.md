# Miya Android 对标验收（2026-11-07 至 2026-11-08）

**验收结论：未通过，尚未达到 iOS 全功能水平。** 用户要求的“全部通过”没有完成，不能发布全部通过声明。以下结果来自真实 Android 模拟器操作；未覆盖项、单测和源码修复均不替代端到端通过。

本轮已修复截图对应的输入队列自锁，并在用户“授权补齐”后实施 Android 原生服务桥、权限、认证、选图、语音、浏览器/PDF/导出、彩色 emoji 和触觉/角标。真实录音到云端、语音自动发送/取消/后台、图片识别、项目 HTML/源码/PDF/导出及 Web 表单/Cookie/SSE/WebSocket 已有端到端证据。立即引导、跨项目引用、交接、真实问题表单、共享记忆和自动化也已补测。系统推送由用户明确暂缓；未完成的用例仍不计通过。

## 环境与证据边界

- 基线：Miya 初测 `ae3b44eafce64c2662c9b41bc0da27388c6563e9`、本次工作树基于 `ac6471e02bcbe6ae8130c5b9ec6b2529dc564348`，mygo `732fa8c86df32d70af798b2d231a519e7eb92336`，加本轮修复（测试产物构建时尚未提交）；Miya replace 指向 `../mygo`。期间其他任务已提交 Pi 开发；本轮修复声明不包含这些改动。
- AVD `mygo_api36`，Android 16/API36、arm64、1080×2400、420dpi、4KB页。初期 SwiftShader；其系统合成/GL出现严重延迟，保留栈后改用 host GPU继续。没有 Android 真机，不推断硬件性能、电量、真实相机/录音和OEM后台行为。
- 隔离端到端包 `com.daodao.miya.androidvalidation`：真实手机 UI、真实本机桌面和协议，临时入口仅提供 UI 自动化与状态观测；不是 ui.Tester 或伪造模型回复。
- 独立产品包 `com.daodao.miya`：`scripts/build-android.sh` 从真实根 main 构建，无 Go overlay、无 QA HTTP 服务；已实际安装并经用户深链冷启动连接桌面，截图有绿色连接状态；第一阶段曾收到 `PROD_MAIN_OK`。当前原生补齐后的最终包另经系统键盘/触摸收到 `PROD_TIMEZONE_FINAL_DONE`，并修正 Android 默认 UTC 导致的八小时时差，见 [最终产品包回复](../.mygo/android-acceptance/product-timezone-final-reply.png)。APK为本机 debug 签名，最低API29；不是发布签名或多版本兼容性验收。
- iOS 对照来自现有实现和 [手机端功能](mobile-features.md)、[多桌面](mobile-desktops.md)、[通知](notifications.md)；本轮未重新执行完整 iOS 套件。
- 本地证据根目录为 [`.mygo/android-acceptance/`](../.mygo/android-acceptance/)，初测见 [原始基线报告](android-validation-2026-11-07.md)。忽略目录不会自动随 Git 提交；完整连接码不写入报告。

## 截图中的 ANR

系统 ANR 记录显示 Java 主线程卡在 `MygoInput.nativeOnTouchBatch`。mygo Android pump 使用容量64的任务通道，输入填满后，pump向自己的队列投递下一帧时等待，Java输入回调也随之等待，形成自锁。改为不阻塞投递的 FIFO，并在持续流量下回收已消费存储。

已用并发/reentrant单测及真实JNI突发验证。原截图对应长会话重新打开后，512次触摸和12次滚动完成，状态接口和后续页面可操作，无新的ANR事件。此结果覆盖本次复现路径，不能等同于长期无ANR。SwiftShader下另有GL提交延迟，host GPU下恢复正常；保留 `resume-goroutines.txt`，尚未把该性能问题计为通过。

## 真正试用发现的共享缺陷

专用QA会话中，添加和删除排队消息有效；按停止后，后续队列仍自动发送。无论在启动期还是等待工具实际运行后停止，均未出现预期的“Continue sending”。源码确认 Codex `turn/completed` 清除了 `StopRequested`，使自动排队恢复。

已删除这一错误清除：只有新发送或用户明确继续才清除暂停。新增回归先在 interrupted/completed/failed 三种结束事件下失败，修复后通过。运行中的用户桌面后台尚未升级。本轮另以新源码构建专用后台，使用 `MIYA_ISOLATED_DIR`、独立存储和 `/private/tmp/miya-android-qa-project` 合成项目完成真实 Android 回归：实际计时子进程运行后排队两条、删一条、停止，队列保持暂停；点 Continue sending 才收到剩余回复，被删除消息未发送。见 [新后台队列验收](../.mygo/android-acceptance/isolated-queue-results.json)。没有强杀、重启或替换用户的 macOS Miya 后台。


暂停自动化的“立即运行”还暴露了另一共享缺陷：实际任务已完成，却因计划为暂停而被标为 Interrupted。现在只允许从暂停状态主动启动的该次运行独立完成/检查重试，不启用其后续计划；正常运行中再暂停的原行为保留。普通、检查重试、工作树回归共40项通过，范围复审无可行动发现。真实新后台手动运行已显示 Passed，计划仍为 Paused，合成任务已删除：[运行页](../.mygo/android-acceptance/isolated-auto-pass.png)、[后台结果](../.mygo/android-acceptance/isolated-automation-result.json)。

实际 MCP report_result 确认卡又复现了空表单错误：手机的 `{}` 因 `omitempty` 在 JSON 传输时消失，后台误报 Complete the form。修复后保留空对象与 nil 的区别，必填字段校验不放宽；旧实现红色回归、修复后绿色，复审无可行动发现。更新 Android QA 包后，同一张真实等待卡已确认成功并返回预期回复：[完成证据](../.mygo/android-acceptance/mcp-confirmation-completed-tree.json)。

## 全功能矩阵

PASS只代表表内明确范围；PARTIAL代表已有部分真实证据；FAIL代表已发现失败或未实现；BLOCKED代表缺少实现/配置；UNTESTED不计通过。41组结果：PASS 16，PARTIAL 20，FAIL 0，BLOCKED 3，UNTESTED 2。这是整组验收统计，不是已通过的单个操作数量。

| 编号 | 功能 | 结果 | 证据与限制 |
| --- | --- | --- | --- |
| F01 | 构建安装启动 | PASS | 真实 main 无 overlay APK 构建、安装、冷启动成功；仅 API36/arm64/debug 包。 [build-production-reviewed.log](../.mygo/android-acceptance/build-production-reviewed.log)；[production-deeplink-launch.log](../.mygo/android-acceptance/production-deeplink-launch.log) |
| F02 | 配对扫码粘贴深链 | PARTIAL | 产品包真实深链冷启动连接；3次 Activity 重建不重复派发 URL。相册与系统相机拍照扫码均真实返回中文、组合 emoji 和 NUL，修正了注入画面的裁切位置。仍为拍照后识别，尚未实现 iOS 连续取景式扫码。 [扫码结果](../.mygo/android-acceptance/native-scanner-results.json)；[相机成功](../.mygo/android-acceptance/native-camera-scan-result.json)；[深链重建](../.mygo/android-acceptance/deep-link-recreation-results.json) |
| F03 | 多桌面隔离管理 | PASS | 真实添加同机隔离后台，来回切换保留隔离桌面中文草稿，原桌面草稿未改变；只删除临时连接后原桌面仍连接。手机两端现有管理页面都只提供添加/切换/移除，未把并不存在的重命名入口列为 Android 缺口。 [切换与清理](../.mygo/android-acceptance/desktop-switch-cleanup-results.json)；[同名桌面目视核验](../.mygo/android-acceptance/desktop-manager-current.png) |
| F04 | 断线重连离线缓存 | PARTIAL | 关闭 Wi-Fi/数据后已有回复仍可查看；恢复原网络后自动重连。离线冷启动及全部缓存分支未覆盖。 [offline-reconnect.json](../.mygo/android-acceptance/offline-reconnect.json)；[offline-cached-reply.png](../.mygo/android-acceptance/offline-cached-reply.png) |
| F05 | 首页分组项目筛选排序 | PARTIAL | 加载真实104个项目及会话分组；筛选/排序全组合未覆盖。 [sidebar-tree.json](../.mygo/android-acceptance/sidebar-tree.json)；[sidebar-live.png](../.mygo/android-acceptance/sidebar-live.png) |
| F06 | 导航返回转场安全区 | PARTIAL | 设置返回、More取消和侧栏切换已操作；全部转场/系统返回组合未覆盖。 [settings-results.json](../.mygo/android-acceptance/settings-results.json)；[conversation-more-tree.json](../.mygo/android-acceptance/conversation-more-tree.json) |
| F07 | 主题字号减少动效旋转 | PASS | API36实测字号1/1.5/2、减少动效实时上报、明暗和横屏；中文及组合 emoji 彩色渲染。截图等待尺寸、主题和像素稳定后采集，系统图标已随实际主题修复。 [appearance-results.json](../.mygo/android-acceptance/appearance-results.json)；[native-emoji-color.png](../.mygo/android-acceptance/native-emoji-color.png) |
| F08 | 中文IME选区复制粘贴撤销 | PARTIAL | 输入框真实全选后系统 Copy 保留选区和草稿；补齐内置输入框选区、反向选择和预编辑无操作回归。Gboard 实际 nihao 按键已入草稿，但等待中文语言包，候选与中文预编辑未通过；粘贴/撤销完整组合仍待测。输入框长按是两端既有的语音手势。 [输入框复制](../.mygo/android-acceptance/selection-editable-result.json)；[Gboard 恢复](../.mygo/android-acceptance/gboard-restore-results.json) |
| F09 | Codex真实收发 | PASS | 真实 Codex UI 发送，收到包含完成标记的回复 [first-send.json](../.mygo/android-acceptance/first-send.json)；[codex-reply-tree.json](../.mygo/android-acceptance/codex-reply-tree.json) |
| F10 | Claude真实收发 | PASS | 真实 Claude Code UI 发送并回复；首轮标记精确匹配脚本失败属于选择器问题 [claude-send.json](../.mygo/android-acceptance/claude-send.json)；[claude-reply-tree.json](../.mygo/android-acceptance/claude-reply-tree.json) |
| F11 | 模型设置默认恢复 | PARTIAL | 打开实际模型列表、当前选择、恢复默认与草稿保留通过；未逐个模型发请求 [settings-results.json](../.mygo/android-acceptance/settings-results.json)；[model-choices.png](../.mygo/android-acceptance/model-choices.png) |
| F12 | 推理强度 | PARTIAL | 实际打开推理选项并选择默认；未遍历所有等级 [settings-results.json](../.mygo/android-acceptance/settings-results.json)；[effort-choices.png](../.mygo/android-acceptance/effort-choices.png) |
| F13 | 权限模式逐条选项 | PARTIAL | 实际打开并选择 Automatic approval；未逐项验证策略执行 [settings-results.json](../.mygo/android-acceptance/settings-results.json)；[permission-choices.png](../.mygo/android-acceptance/permission-choices.png) |
| F14 | 排队删除暂停继续 | PASS | 新源码隔离后台已实测排队两条、删除一条、实际命令运行后停止、暂停保持、手动继续后回复；删除项未发送。用户原后台尚未切新版。 [isolated-queue-results.json](../.mygo/android-acceptance/isolated-queue-results.json)；[isolated-queue-paused.png](../.mygo/android-acceptance/isolated-queue-paused.png) |
| F15 | 立即引导 | PASS | 真实20秒计时子进程运行中，Android点 Steer now，更新进入当前回合而非队列，收到 ANDROID_STEER_NOW_DONE。 [isolated-steer-results.json](../.mygo/android-acceptance/isolated-steer-results.json) |
| F16 | 停止隔离 | PARTIAL | 真实 Stop 已触发；队列暂停失败另见F14，跨会话隔离全部分支未覆盖。 [queue-stop-running-results.json](../.mygo/android-acceptance/queue-stop-running-results.json) |
| F17 | 执行审批 | PASS | 隔离 QA 会话真实触发只打印固定标记的执行请求，Android Deny 后未执行并返回拒绝标记；另一请求 Allow once 后实际打印并收到 ANDROID_EXEC_ALLOWED_DONE。不读写用户文件。 [拒绝](../.mygo/android-acceptance/exec-approval-denied-result-tree.json)；[允许](../.mygo/android-acceptance/exec-approval-allowed-result-tree.json) |
| F18 | 问题回答保密跳过 | PARTIAL | 真实 Claude AskUserQuestion：未填写时提交禁用；Blue单选、中文/组合emoji自由回答、跳过均经后台确认并返回对应回复。保密问题未覆盖。 [question-choice-tree.json](../.mygo/android-acceptance/question-choice-tree.json)；[question-free-result-tree.json](../.mygo/android-acceptance/question-free-result-tree.json)；[question-skip-result-tree.json](../.mygo/android-acceptance/question-skip-result-tree.json) |
| F19 | MCP表单和URL确认 | PARTIAL | 真实空字段 MCP report_result 确认卡复现传输丢空对象，已修并在 Android 原卡通过；必填校验回归通过。含字段和URL确认的真实链路尚未覆盖。 [mcp-confirmation-completed-tree.json](../.mygo/android-acceptance/mcp-confirmation-completed-tree.json)；[empty-mcp-form-red.log](../.mygo/android-acceptance/empty-mcp-form-red.log) |
| F20 | ComputerUse访问审批 | UNTESTED | 尚未完成真实 Android 端到端用例，不计通过。 |
| F21 | 系统选图多选取消 | PARTIAL | 真实系统 Photo Picker 单图、多选两图、取消和私有缓存导入通过；两份导入与输入字节一致，调用方清理缓存。选择器重建全组合未覆盖。 [photo-multiselect-results.json](../.mygo/android-acceptance/photo-multiselect-results.json)；[native-dialog-results.json](../.mygo/android-acceptance/native-dialog-results.json) |
| F22 | 图片传输预览缓存 | PARTIAL | 真实发送测试图片，模型正确描述蓝色背景、红色方块、黄色圆形并回复 IMAGE_NATIVE_DONE；缩略图重启缓存等分支未全覆盖。 [image-real-reply-tree.json](../.mygo/android-acceptance/image-real-reply-tree.json)；[image-real-reply.png](../.mygo/android-acceptance/image-real-reply.png) |
| F23 | 语音采集云端定稿 | PASS | 官方模拟器音频注入 → 真实 AudioRecord 16kHz/mono/PCM16，采集400384字节，非静音；Miya Android 实际云端定稿包含“蓝色背景”“红色方块”。宿主麦克风保持关闭，未冒充真机录音。 [audio-record-results.json](../.mygo/android-acceptance/audio-record-results.json)；[voice-ui-result.json](../.mygo/android-acceptance/voice-ui-result.json) |
| F24 | 语音插入自动发送取消后台 | PARTIAL | 关闭自动发送保留前缀并插入定稿；开启后真实发送且收到 VOICE_AUTO_NATIVE_DONE；取消和 Home 后台均保留原草稿。开关已恢复关闭；全部选区/权限等待分支未全覆盖。 [voice-branches-results.json](../.mygo/android-acceptance/voice-branches-results.json)；[voice-auto-reply.png](../.mygo/android-acceptance/voice-auto-reply.png) |
| F25 | 文件浏览下载Range路径边界 | PARTIAL | 真实目录、HTML相对图片/导航、源码、两页PDF、系统导出/取消已测。导出文件921字节/SHA256一致；重建后继续导出成功。Range/HEAD/只读方法、越界、内外符号链接和非普通文件检查通过。保存器长停留冻结后一次代理错误页需Reload恢复，保留限制。 [browser-export-results.json](../.mygo/android-acceptance/browser-export-results.json)；[browser-export-recreated-results.json](../.mygo/android-acceptance/browser-export-recreated-results.json)；[browser-boundary-results.xml](../.mygo/android-acceptance/browser-boundary-results.xml) |
| F26 | Web发现APICookieSSEWebSocket | PARTIAL | 手动端口经真实手机—桌面代理，HTML、Cookie、重定向、表单POST、SSE、双向WebSocket、前进后退/刷新/关闭均通过；自动发现与全部会话运行时筛选未全覆盖。 [web-live-results.json](../.mygo/android-acceptance/web-live-results.json)；[web-live-transports.png](../.mygo/android-acceptance/web-live-transports.png) |
| F27 | 远程桌面授权控制撤销 | BLOCKED | 完整远程桌面控制和撤销涉及 Mac 授权与真实窗口操作，尚未完成，不计通过。没有通过测试偷偷授予 OS 权限。 |
| F28 | 认证保护桌面解锁 | PARTIAL | 真实系统 PIN 回退成功，未录入、取消、忙碌、context截止和Home失活分支已测。临时PIN已清除、恢复无锁屏；指纹录入被自动审批要求单独授权，未录入，故生物识别成功仍未验收。 [auth-passcode-result.json](../.mygo/android-acceptance/auth-passcode-result.json)；[auth-security-restored-query.json](../.mygo/android-acceptance/auth-security-restored-query.json) |
| F29 | 跨项目会话引用 | PASS | 真实@选择不同项目的Miya QA会话后发送，模型返回新指令未提供的源会话首条完成标记 ANDROID_QA_REPLY_DONE，证明引用上下文已传入。 [reference-cross-project-result-tree.json](../.mygo/android-acceptance/reference-cross-project-result-tree.json) |
| F30 | 消息选区三格式复制 | PASS | 补齐原生 Markdown 剪贴板、系统浮动菜单与拖动手柄。真实长按消息、跨三段拖动后 Copy，纯文本/HTML/Markdown 精确保留中文、组合 emoji、空行和加粗；只读复制清除选区。独立 UID 通过标准 ContentResolver 读取 Markdown，源进程退出后仍可读，写入和其他 URI 均拒绝；三种外部 Markdown MIME 和空值/超限失败均验证。 [跨段复制](../.mygo/android-acceptance/selection-cross-paragraph-result.json)；[原生菜单](../.mygo/android-acceptance/selection-cross-paragraph-expanded.png)；[跨应用](../.mygo/android-acceptance/clipboard-cross-app-results.json) |
| F31 | 错误回复重试 | UNTESTED | 尚未完成真实 Android 错误卡片→重试回复→恢复输出的端到端用例，不计通过。手动编辑消息与文件回退是桌面能力，iOS/Android 手机现有实现均不提供，不作为 Android 独有缺口。 |
| F32 | 交接新会话 | PASS | 真实点 Hand off to a new conversation，中文草稿和引用保留，新会话得到源会话未在指令中写明的完成标记及 HANDOFF_NATIVE_DONE。 [handoff-results.json](../.mygo/android-acceptance/handoff-results.json)；[handoff-draft-preserved.png](../.mygo/android-acceptance/handoff-draft-preserved.png) |
| F33 | 共享记忆只读 | PASS | 真实Miya项目记录正文、类型/状态、日期、来源、来源会话跳转、用户偏好切换和刷新已验证；未修改记忆。此前空列表是所选作用域不同。 [memory-miya-records-tree.json](../.mygo/android-acceptance/memory-miya-records-tree.json)；[memory-source-conversation.png](../.mygo/android-acceptance/memory-source-conversation.png)；[memory-user-verified-tree.json](../.mygo/android-acceptance/memory-user-verified-tree.json) |
| F34 | 自动化创建运行结果 | PASS | 真实手机生成设置、保存暂停任务、手动运行、展开报告、跳转运行会话通过。修复暂停误判与空MCP表单后，新后台最终Passed且计划仍Paused。测试任务均已删除，原两条业务任务未运行/改动。 [automation-created-tree.json](../.mygo/android-acceptance/automation-created-tree.json)；[isolated-auto-pass-tree.json](../.mygo/android-acceptance/isolated-auto-pass-tree.json)；[isolated-fixture-cleanup.log](../.mygo/android-acceptance/isolated-fixture-cleanup.log) |
| F35 | 额度查询刷新 | PARTIAL | 真实额度弹层显示 Weekly quota / 20% used；刷新和其他代理额度未覆盖。 [quota-tree.json](../.mygo/android-acceptance/quota-tree.json)；[quota-live.png](../.mygo/android-acceptance/quota-live.png) |
| F36 | 系统推送点击角标 | BLOCKED | FCM/厂商配置与远程推送实现未完成，用户已要求先测其他功能。原生静默角标通知 number=3 和清除已验证；不替代真实推送。 [effects-results.json](../.mygo/android-acceptance/effects-results.json)；[effects-native-notification.txt](../.mygo/android-acceptance/effects-native-notification.txt) |
| F37 | 未读跨端同步 | PASS | 真实8秒任务运行后手机切到另一会话；完成时桌面共享未读版本产生、手机侧栏显示，重新打开该会话后收到完整回复并将桌面共享未读清零。此流程不依赖推送。 [读回执结果](../.mygo/android-acceptance/unread-sync-results.json)；[未读侧栏](../.mygo/android-acceptance/unread-completed-sidebar.png) |
| F38 | 后台重启缓存凭证 | PARTIAL | 正常应用重启后自动恢复配对并连接桌面；Keystore另有初测。完整离线冷启动/缓存迁移未覆盖。 [language-after-app-restart.json](../.mygo/android-acceptance/language-after-app-restart.json) |
| F39 | 本机语言与时间 | PASS | 简体中文/英语切换与重启后语言保留通过，已恢复英语。实际产品截图另发现 Go Android 默认 UTC 导致消息慢8小时；读取系统时区并加载 OS tzdata 后，同一消息由7:02 PM变为3:02 AM，新发送也显示正确本地时间。更改系统时区后需重启取得新的 Go Local 快照。 [语言](../.mygo/android-acceptance/language-chinese.json)；[修正后](../.mygo/android-acceptance/product-timezone-final-reply.png) |
| F40 | TalkBack | BLOCKED | TalkBack 已安装，但临时启用会赋予屏幕读取与操作能力，已另行询问用户，尚未得到回答；没有自行启用，不计通过。 |
| F41 | 长会话ANR回归 | PARTIAL | 修复后原长对话512次JNI原生触摸+12次滚动，状态与UI可响应、无新ANR。SwiftShader曾GL提交严重延迟；host GPU恢复。未作长期/真机稳定性验收。 [long-thread-after-stress.json](../.mygo/android-acceptance/long-thread-after-stress.json)；[long-thread-after-stress.png](../.mygo/android-acceptance/long-thread-after-stress.png) 本轮 QEMU 宿主另发生 EXC_BAD_ACCESS 崩溃并重开，不作为 Miya ANR 或长期稳定性通过的证据：[宿主崩溃](../.mygo/android-acceptance/emulator-host-crash.json)。 |

## 已实施的源码修复与验证

Miya：Android手机入口、JNI启动及Java网络接口 getter；真实APK构建脚本、标识/图标/miya深链打包；Codex停止队列修复。mygo：非阻塞pump队列及回收、surface启动时序/EGL恢复、生命周期回放、扫码取消完成一次、线程安全扫码结果队列、Context私有路径和字号/减少动效/网络接口上报、冷/热URL转发。自动化插件增加仅在Unix私有目录启动的显式Start，用于共享库宿主验收；产品APK不链接该QA入口。

- `go test -race ./internal/android/backend ./plugins/automation` 通过；持续队列内存回收、同名socket冲突、长路径和不洁路径拒绝有回归。
- Miya 手机端集成测试与Codex停止/队列/流事件相关测试通过，见 `stop-and-mobile-regression.log`；这些是逻辑回归，不冒充Android端到端。
- Miya全仓 `CGO_ENABLED=0 go build ./...` 通过，Linux/Windows自动化插件无cgo交叉编译通过；脚本 `bash -n` 及限定补丁 `git diff --check` 通过。
- mygo桌面 `CGO_ENABLED=0` counter-native 和 hello 构建通过；体积前后分别保持9,869,042和9,750,354字节，无增长。
- 按 [review-it 技能](/Users/daodao/.agents/skills/review-it/SKILL.md)审查未提交改动；接纳并修复EGL恢复、持续队列存储、运行时umask与socket路径问题。最终范围复审及Miya停止逻辑复审无可行动发现。Android入口复审另发现空地址网卡的nil AltAddrs会回退netlink，已改为非nil空切片并复审通过。

## 仍未完成的验收与边界

用户已“授权补齐”，原生服务桥及上述能力已实际实施，不再处于等待原生实现授权状态。第一轮及第二轮审查发现的浏览器回调、导出重建、消息销毁、权限最小范围、扫码授权及深链重放问题已逐项修复；mygo 第7轮原生生命周期复审、Miya后台资源保留复审及新增自动化/空MCP表单复审均已无可行动发现。SDK主机路径与跨平台麦克风提示的Miya复审已无可行动发现。新增API使用按旧版系统加判断，mygo宿主最低版本未改变；Miya产品包仍为API29起，实际只验API36。

Android远程推送由用户暂缓。指纹成功需要先获准在测试AVD中临时录入模拟指纹；目前未录入，原PIN回退测试的临时凭据也已清除。运行中的桌面后台没有升级，共享Codex队列暂停修复已在新源码隔离后台端到端通过，原用户后台仍未更新。全部问题/MCP 分支、远程桌面、TalkBack 及表中其他未覆盖用例仍不能计通过。Android 三格式剪贴板及消息选区已补齐并实测。

保存窗口长时间停留时，Android缓存进程冻结会暂停QA HTTP端口；已通过系统`isFrozen=true`确认，不能误报为Go死锁。早期返回时第一次请求曾遇到代理离线页，Reload 后恢复。已修正 Android 文件浏览期间的连接保留；随后真实 HTML→PDF→系统保存产生正常后台事件，连接保持、导出字节一致、取消/重载/关闭通过（[后台保存回归](../.mygo/android-acceptance/browser-background-results.json)）。长时间冻结及重建后的全部恢复组合仍未覆盖。

桌面构建体积复测仍为counter-native 9,869,042、hello 9,750,354字节。Miya `voiceinput/mobile/locales` 针对性22项回归通过，mygo backend/text 345项回归通过。官方Android API资料确认了[PDF原生渲染](https://developer.android.com/reference/android/graphics/pdf/PdfRenderer)、[下载回调](https://developer.android.com/reference/android/webkit/DownloadListener)、[声明CAMERA后的委托拍照授权要求](https://developer.android.com/privacy-and-security/minimize-permission-requests)及[API29起自有媒体无需读取图库](https://developer.android.com/training/data-storage/shared/media)。这些资料不替代实际运行证据。

本轮源码随报告提交到主分支；模拟器保留最终产品界面，桌面服务未强制切换。APK和本地证据保留在忽略目录，未推送远程或发布。

**下列为第一阶段产品包，不包含随后原生补齐，不能当作本阶段最终产物。** 第一阶段APK的v2签名验证通过，SHA-256：`c870fbc3865af51e76f8fd752a6576cad6c07fc62407eea6d713c5f17897b3d9`。产物及环境记录见 [artifacts-final.json](../.mygo/android-acceptance/artifacts-final.json)。

Android 剪贴板 JNI 另修复成功布尔值反转，读操作改为单次快照，Markdown 使用受限、只读 FileProvider URI；跨应用实测和第9轮范围复审通过。消息选区补测暴露的原生菜单/手柄缺口已实现。第4轮范围复审无可行动发现；backend/ui/richtext 回归及 backend/text/automation race 回归通过。实际长草稿全选、端点离屏、Home 后恢复复制、内置搜索控件手柄缩选和复制、选区显示时 Activity 重建均已补测，见 [长草稿恢复](../.mygo/android-acceptance/selection-long-editable-state.json)、[内置控件](../.mygo/android-acceptance/selection-builtin-search-result.json)、[重建](../.mygo/android-acceptance/selection-recreate-result.json)。宿主麦克风关闭状态再次核验，扫码仅使用合成二维码画面。

最终独立产品包由真实根 main 构建，版本 0.1.0、minSdk29/targetSdk36；v2 签名验证通过，已更新安装并冷启动连接原桌面，APK 中无 QA Application/Receiver/观测入口标记。 [最终构建记录](../.mygo/android-acceptance/build-product-final-timezone.log)、[签名](../.mygo/android-acceptance/product-final-signature.txt)、[产物元数据](../.mygo/android-acceptance/artifacts-product-final.json)。SHA-256：`25c502e32ce417b28d2c3430d343490a4fae608e2d3ecd832c037080051e7afa`。产品包已通过系统键盘/触摸真实收发；实际长按回复→Copy 后，独立 UID 经标准 ClipboardManager/ContentResolver 精确读出纯文本/HTML/Markdown，并确认只读 URI 及兄弟 URI 拒绝，见 [产品包跨应用复制](../.mygo/android-acceptance/product-final-cross-uid-clipboard.json)。该次复制验证发生于只增加时区初始化前的同源码产品包，构建指纹在元数据中另行保留；最终时区版又实际发送并收到 `PROD_TIMEZONE_FINAL_DONE`。

隔离测试后台已通过受隔离目录保护的 quit API 优雅退出，原 macOS 后台未安装/替换/强制切换；原桌面连接和草稿保持。见 [后台清理](../.mygo/android-acceptance/isolated-backend-cleanup.json)。

最后收尾新增 [mygo Android 本地时区初始化](../../mygo/android/jni/local_time.go)，只在 Go 包初始化期间设置 Local，使用系统 tzdata，不引入桌面依赖或内嵌时区库。该独立补丁范围复审无可行动发现；最终产品 APK 冷启动后同一缓存消息与新回复的本地时间均已目视验证。已安装 APK 的 SHA-256 与本次产物完全相同，ELF 4个 LOAD 段均16KB对齐；实际运行仍只有4KB页的API36 AVD，不冒充16KB真机验收。两个临时测试包和QA端口已清理，模拟器保留最终Miya会话：[清理记录](../.mygo/android-acceptance/test-package-cleanup.json)。

源码提交记录：mygo 主分支 `dbb97f5b0fc29613f3ab55f9f2103d937a027750` 包含本轮 framework 修复；Miya 通过既有 `replace => ../mygo` 使用该工作区。产物元数据中的基线提交仍保留构建时值，不重写历史指纹。提交代码不改变本报告“尚未达到 iOS 全功能水平”的结论。
