> 本文是初轮基线记录，“未改源码/未收发”等描述仅适用于当时。后续已进行真实试用与源码修复，当前结论和验收矩阵请见 [最新验收报告](android-acceptance-2026-11-07.md)。

> 本文记录原始基线，后续补齐与最终实测结果见 [Android 对标验收](android-acceptance-2026-11-07.md)。

# Android 模拟器可用性验证（2026-11-07）

结论：**尚未达到 iOS 水平。当前 Miya 不能通过现有构建链直接得到可启动的 Android 应用；mygo 的 Android 原生 UI 已有可用基础，但启动、沙箱目录、系统能力和部分文本体验仍有缺口。**

本轮是验证，没有修改 Miya 或 mygo 的产品代码，没有安装或重启 macOS Miya，也没有操作其后台服务。新增此报告，测试代码、APK、截图和原始日志保存在仓库忽略的 `.mygo/android-validation-20261107/`。

## 环境与测试边界

| 项目 | 实测基线 |
| --- | --- |
| Miya | `ae3b44eafce64c2662c9b41bc0da27388c6563e9` |
| mygo | `732fa8c86df32d70af798b2d231a519e7eb92336`；Miya 使用 `replace => ../mygo` |
| Go | `go1.27.3 darwin/arm64` |
| Android | 已有 AVD `mygo_api36`，Android 16 / API 36，arm64-v8a |
| 设备 | 1080 × 2400，420 dpi，4 KB 内存页 |
| 渲染 | Emulator 37.2.12，SwiftShader；不用于推断真机帧率、功耗或 GPU 性能 |
| 工具链 | SDK 36、NDK 28.2.13676358、JDK 17、Gradle 9.1.0、AGP 9.0.1 |
| 隔离包名 | `com.daodao.miya.androidvalidation`，没有覆盖已有 `org.mygo.host` |

测试分成三层：原始 Miya 源码编成 c-shared；使用 mygo Android 宿主和临时 JNI 启动桥调用 Miya 原有 `MobileEntry()`；使用相同框架和真实 Miya 会话页面的离线夹具验证输入和布局。后两层的成功**不代表原始应用可发布**。宿主是复制到测试目录后改的包名，未修改 mygo 源码；会话、项目和消息均为合成数据，没有向真实会话发送消息。

iOS 对照来自当前实现和既有验收文档（[手机端功能](mobile-features.md)、`../mygo/docs/mobile-progress.md`）；本轮没有重新执行 iOS 真机或模拟器全套验收。

## 实测结果

| 能力 | Android 结果 | 证据及与 iOS 的差距 |
| --- | --- | --- |
| 标准 CLI 构建 | **失败** | `go tool mygo build -platform android/arm64` 返回 unsupported build platform；iOS 有完整打包分支 |
| 原始 Miya APK 启动 | **失败** | 手工编译原始 `.so` 并打包后，启动报 `UnsatisfiedLinkError: MygoIme.bindView`；原始动态库无 Android JNI 导出 |
| 手机端入口 | **未接入** | `main.go:13` 仅 `GOOS == "ios"` 调用 `MobileEntry()`；Android 仍走桌面入口 |
| 原生渲染 | 有条件通过 | 临时接启动桥后配对页、会话页、中文、Markdown 代码高亮和表格正常显示 |
| 冷启动稳定性 | **未通过** | 夹具首次启动捕获 `backend android/egl created no surface for the window's Content` 并 SIGABRT；随后重启和连续 5 次冷启动成功，不能据此排除竞态 |
| 冷启动生命周期 | **失败** | 连续 5 次窗口已前台可用但 `App.Lifecycle()` 均为 `inactive`；切后台再回来才成为 `active` |
| 基础输入 | 通过已测范围 | 明确聚焦后，Android 键事件写入 `Android_123`；真实点击 Gboard 输入 `qwe`、粘贴中文候选、回车换行，Go 草稿与屏幕一致 |
| 软键盘布局 | 通过已测范围 | IME bottom inset 从 24 变为 336 DIP，输入框让位；系统返回键收起键盘 |
| 中文/HTML 剪贴板 | 通过已测范围 | 文本 `Android 中文 👩🏽‍💻\nMiya` 和 HTML `<b>Miya 中文</b>` 读写一致；启动期间系统弹窗干扰下的空读已单独排除 |
| 硬件粘贴键 | 未通过该路径 | `KEYCODE_PASTE`（279）记录为 unmapped；实际软件键盘候选粘贴成功 |
| Keystore | 通过已测范围 | Unicode、二进制 `[0 1 255]`、删除及删除后 not found 均符合预期；5 次新进程启动均恢复测试密钥 |
| 前后台恢复 | 通过已测范围 | 10 次 Home → 回前台，均收到 background/active、同一 PID、草稿保留；最终截图没有持续黑屏 |
| 深色模式 | 通过已测范围 | 系统切换后 `Theme.IsDark()` 为 true，页面变黑底；恢复后正常 |
| 横竖屏 | 通过已测范围 | 收到 2400 × 1080 surface 和横屏安全区；恢复竖屏，页面仍可操作 |
| 系统字号 | **失败** | 系统 `font_scale=1.5` 并完成 Activity 重建后，`Theme.Preferences().TextScale` 仍为 1，正文未随字号放大 |
| Emoji 绘制 | **失败** | 草稿 JSON 中保留 `👩🏽‍💻`，消息中保留 `👩🏽‍💻`、`🔐`，截图对应位置留白；存储/输入编码与显示问题应分别处理 |
| 照片二维码解码 | 通过已测范围 | 系统 Photo Picker 选择测试二维码后，Go 返回 `miya-android-validation-20261107`，error 为 nil |
| 扫码体验/取消 | **部分支持，有故障** | Android 为选照片/调用系统相机后解码，未提供 iOS 同等实时取景；用户返回取消成功。测试 context 截止后一次调用仍悬挂，HTTP 40 秒未返回；后续独立请求能正常解码 |
| 用户数据/缓存目录 | **失败** | `PathUserData`、`PathCache` 返回 `neither $XDG_* nor $HOME are defined`；默认 temp 为 `/data/local/tmp`，应用 UID 的可写检查返回 1 |
| 麦克风/语音输入 | **未实现** | mygo 麦克风权限 Query 返回 unsupported；Miya Android 命中 `capture_other.go` 的 unavailable 实现 |
| 生物认证 | **未实现** | `Biometrics.Query()` 返回 unsupported，无法达到 iOS 设备所有者认证体验 |
| 推送、角标、触觉 | **未实现** | `RegisterPushNotifications`、`SetNotificationBadge`、`Haptics.Play` 均返回 unsupported；不等同于“所有本地通知都不可用” |
| 文件选择/应用内浏览器 | **未实现** | `Dialog.Open` 返回 Android unsupported，`OpenInAppBrowser` 返回 unsupported，影响附件选择及项目文件/预览流程 |
| 网络路径监测 | **未实现** | `Network.PathStatus()` 返回 unsupported；不代表普通 Go 网络请求不可用 |
| `miya://` 深链 | **当前宿主未接入** | 对隔离包执行 ACTION_VIEW 无法 resolve；Android 没有消费 Miya `urlSchemes` 配置的打包流程 |
| 无障碍 | 未验收 | UIAutomator 树未暴露 Miya 子控件；框架存在 accessibility bridge，尚未完成 TalkBack 验收，不能据此断言完全不支持 |

基础输入最终草稿（保持了原有组合 emoji，并加入实际输入内容）：

```text
中文 English 👩🏽‍💻Android_123qwe<b>Miya 中文</b>
 多行输入
```

首轮 `adb input text` 在未明确聚焦/事件尚未处理完时读到旧值，不计入失败；最终结果按内容条件等待，并用真实 Gboard 点击交叉验证。中文拼音预编辑、第三方输入法、选区手柄与跨段复制尚未验收。

## 需要优先处理的问题

1. **接通可重复的构建和启动链。** Miya 增加 Android 手机入口和 `jni.Start`，mygo CLI 提供 Android 宿主生成、APK 构建、安装及诊断；配置独立 applicationId、资源、权限及深链。
2. **修复 mygo 初始化与取消契约。** `android/jni/jni.go:79` 在 `OnSurfaceCreated` 入队前启动应用；`Backend.Init`/pump 可提前发布 Ready，使 `NewWindow` 取不到 surface。Ready 应以可用 surface 为条件。初始 onStart/onResume 早于 App.Init，需保存并回放状态。`backend/mobile.go:70` 的扫码 cancel 删除回调却不完成公共 `Scanner.ScanCode` 等待，需保证取消完成且只完成一次；扫码轮询的一分钟退出同样需要明确完成结果。
3. **接齐 Android 沙箱和 Miya 核心系统能力。** 从 Android Context 初始化私有 files/cache/temp 路径；实现语音采集、权限、生物认证、附件选择、文件浏览/预览、推送和网络变化监测。Keystore 已有基础，不必重复实现。
4. **补齐文本体验和回归覆盖。** 修复彩色 emoji、系统字号与粘贴键映射；覆盖中文 IME 预编辑、长按选择、TalkBack、取消/超时、Activity 重建及不同 Android 版本。

当前适合继续开发与框架验证，不应标为“Android 已达到 iOS 水平”或可发布版。未完成桌面配对后的真实消息收发、断网重连、远程桌面全链路；也未测试真实相机/麦克风、生物认证硬件、FCM/OEM 后台推送、Android 7–15、平板和 16 KB 页设备。ELF 的 LOAD 段均为 `2**14` 对齐，仅证明构建产物对齐，不能替代 16 KB 系统运行测试。

## 回归与证据

已有测试均通过，但它们是桌面进程中的逻辑测试，不是 Android 模拟器端到端套件：

```sh
# mygo：46 个顶层测试，含子测试共 191 个通过
cd ../mygo
go test ./internal/android/backend

# Miya：手机端集成逻辑回归通过
cd ../Miya
go test ./tests/integration -run '^TestMobile' -count=1
```

证据目录：[`.mygo/android-validation-20261107/`](../.mygo/android-validation-20261107/)。此目录被 Git 忽略，仅保留在本机，不随报告提交；`artifacts.json` 记录版本和产物哈希。

| 证据 | 文件 |
| --- | --- |
| CLI 拒绝、原始源码构建、原始启动崩溃 | `cli-build.log`、`build-original.log`、`original-crash.log` |
| 测试启动桥与构建方式 | `harness/main.go`、`build-harness.sh`、`host/`；`pairing` 模式调用原有 `MobileEntry()`，`fixture` 模式使用离线消息 |
| 启动竞态、冷启动状态 | `fixture-startup-failure.log`、`cold-start-results.json`、`cold-starts-runtime.log`、`cold-starts.py` |
| 原生界面/emoji | `pairing-clean.png`、`fixture-cold.png`、`dark-large-font.png`、`landscape.png` |
| 输入法/剪贴板 | `input-software-keyboard.json`、`input-software-keyboard.png`、`clipboard.json` |
| 字号及平台能力 | `large-font-after-recreation.json`、`fixture-cold.json`、`pairing-runtime.log` |
| 前后台 | `lifecycle-results.json`、`after-ten-resumes.png`、`lifecycle.py` |
| 扫码 | `scanner-picker.png`、`scan-final-result.json`、`fixture-runtime.log`、`scan.py`；早期 `scan-*-result.json` 含失败尝试，保留且不算通过 |
| 逻辑回归 | `mygo-backend-tests.json`、`mygo-backend-tests.log`、`miya-mobile-tests.log` |

首次模拟器启动出现过 **System UI isn't responding**，在操作等待并确认系统完成启动后继续验证；它是环境事件，没有归因为 Miya 崩溃。原始 Miya JNI 崩溃与框架 surface panic 均有独立进程日志支持。

## 本机真实桌面连接补测

用户随后提供本机桌面连接码并要求在模拟器连接。Mac 侧对同一地址的 Tailcat 隧道检查成功，排除了连接码无效、桌面未在线及“同机运行必然冲突”的解释。Android 初始失败的底层错误为：

```text
netmon.New: route ip+net: netlinkrib: permission denied
```

Tailscale 的 Android cgo 集成要求应用提供网络接口 getter；当前 Miya/mygo 组合没有注册它。Android app UID 无权通过 Go 默认 netlink 路径枚举接口，隧道在初始化时即失败。这是新增的核心连接阻塞，普通 UI 与逻辑回归无法覆盖。

为完成用户要求，在忽略目录 `connect/` 中建立临时测试入口和 Go build overlay：补私有 files/cache/temp 目录；将用户链接交给原有 `m.pair` 流程；将 JNI 应用启动延后到 surfaceChanged 的 pump 任务之后；保留底层错误；注册从 UDP socket 路由选择获取本机 IPv4 的单路由接口 getter。这个 getter 使用内核选路而不发送 UDP 数据包，适合当前模拟器验证；正式实现仍需 Android Java 网络接口及网络变化回调，不能据此宣称多网络/VPN/蜂窝切换已验收。

修改仅用于测试构建，Miya 和 mygo 产品源码未改。加入适配后，模拟器成功连接 `daodao-mbp.local`，同步 **104 个项目**，`connected=true`，连接错误为空。没有向真实会话发送消息；消息收发、附件、远程桌面和断网重连仍需独立验收。连接状态证据为 `.mygo/android-validation-20261107/connect/connected-state.json`，可连接的测试包为 `.mygo/android-validation-20261107/miya-connect.apk`。连接码不写入报告。

模拟器按用户后续要求保持可见并连接桌面，未关闭。
