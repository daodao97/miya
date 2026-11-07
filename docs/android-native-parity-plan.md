# Android 原生能力补齐：已授权的改动范围

当前 Android 的真实 Codex 消息发送和回复已通过；权限、生物认证、照片附件、项目文件与 Web 预览、语音采集等能力仍有未实现项。仅运行测试无法使这些项目通过，必须新增原生实现。

自动审批审查拒绝了一次同时修改 mygo Android 后端、JNI、权限清单及服务桥的操作，理由是改动范围大、扩大安全与崩溃影响面，且尚未验证。本文件将拟议改动与验收方式具体列出。用户随后明确回复“授权补齐”，已授权按下列范围分功能实施和隔离验证。系统推送配置仍需独立确认。

## 拟议实现

| 功能 | 具体系统实现 | 主要改动位置 |
| --- | --- | --- |
| 异步原生服务 | Go 发出带编号的请求，Java 主线程异步处理，JNI 回调投递到 Go pump；取消完成一次，不互相同步等待 | mygo `internal/android/backend/services_android.go`、`android/jni/services.go`、`Native.java` |
| 权限 | Android runtime permission 查询、申请、拒绝后重试与打开应用设置；按实际功能声明录音、照片、相机、通知等权限 | `MygoServices.kt`、`MygoActivity.kt`、`AndroidManifest.xml` |
| 生物认证 | 系统 BiometricPrompt、设备密码回退、未录入、取消、后台失活等结果；不获取设备密码 | Android 服务桥与 `Authenticator` 后端 |
| 附件和文档 | Photo Picker / Storage Access Framework，将用户选择的文件复制到应用私有缓存；分享导出使用 FileProvider | Android Dialogs 后端、系统结果转发、缓存路径声明 |
| 文件与 Web 预览 | 应用内 WebView 容器，导航、刷新、关闭、下载/导出；只对本机代理域名允许明文 HTTP，保留远程通道的项目边界 | Android browser 服务与 `mobile_experience_android.go`、网络安全配置 |
| 语音 | AudioRecord 采集 16 kHz 单声道 PCM，对接 Miya 已有识别链路；松手结束、取消、切后台和超时均释放录音资源 | Android 音频服务、Miya `capture_android.go` |
| 触觉与角标 | 系统触觉 API、支持的 launcher notification badge；不将不存在的硬件反馈记为实测通过 | Android Mobile 后端 |
| Emoji | 补齐 Android 系统彩色字体栅格化，保留中文和已有非彩色文本路径 | mygo Android text engine 与 native raster bridge |
| 系统推送 | 需要独立确认 FCM/厂商推送配置及后端；不能将本地通知或伪造 token 当作系统推送通过 | 待服务配置后单独设计与验收 |

## 实施约束

1. 先完成隔离测试包验证，再按功能小批次修改、检查和回归；不重启或强杀正在执行任务的桌面后台。
2. 系统权限由真实 Android 弹窗申请；拒绝分支也必须通过。不以 root UID 运行被测应用，不禁用 TLS 验证，不绕过生物认证。
3. 原生对话框、录音、下载与回调都有明确的取消和资源释放路径；新增功能不得改变 iOS 和桌面的行为。
4. 每项保留真实 UI 操作、系统结果、Go 状态和截图/日志；模拟数据、逻辑单测和硬件/服务未覆盖项单独标注。
5. 完成后执行代码审查、针对性回归和桌面体积检查。未验证或未实现的项目不计入通过。

## 验收用例

- 权限：首次允许、首次拒绝、已拒绝、设置中重新允许、并发请求和 Activity 重建。
- 认证：可用性、未录入、成功、拒绝/取消、context 截止、切后台、密码回退；模拟器与真机证据分开。
- 附件：系统选图、多选、取消、原图发送、桌面接收和重新打开后的缩略图缓存。
- 文件：真实项目目录、HTML/图片/PDF/源码、相对资源、下载/导出、Range、越界与符号链接拒绝、关闭后的代理回收。
- Web：本机项目服务、表单/API、Cookie、SSE/WebSocket、重定向与关闭连接；不提交真实业务操作。
- 语音：真实 PCM 采集及云端最终文本、光标插入、自动发送关闭/开启、松手、取消、后台、拒绝权限、失败时保留草稿。
- Emoji/字号：中文、组合 emoji、肤色、混排、选择复制和 1/1.5/2 倍字号，明暗和横竖屏。
- 通知：前台、后台、退出后送达和点击定位；缺少真实推送配置时该项保持未验收。
