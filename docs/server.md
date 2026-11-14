# Miya 服务器版

Linux 服务器运行 `miya -serve`，电脑端和手机端通过 Tailcat 配对连接。无需安装 Tailscale 客户端或开放公网入站端口。Agent、项目、会话、审批、记忆、自动化、项目文件和终端都留在服务器，客户端断开后 Agent 继续执行。

## 构建与安装

从 Miya 源码目录构建 Linux amd64 或 arm64 安装目录。需要仓库 `go.mod` 指定的 Go 版本和 Python 3（读取应用版本；生成升级包需 Python 3.11 及以上），开发检出仍按仓库约定使用相邻的 `../mygo` 模块。

```sh
./scripts/build-server.sh amd64
# ARM 服务器改用：./scripts/build-server.sh arm64
```

将 `build/server/linux-amd64/` 整个目录复制到服务器。使用运行 Agent 的普通用户安装；先在该用户环境中安装并登录 Codex / Claude Code，准备项目目录。当前安装包使用 glibc 动态加载器，适用于 Debian / Ubuntu 等 glibc Linux，Alpine / musl 不在本次支持范围。服务器二进制不需要 GTK、WebKit、图形会话或 cgo；需要正常出站联网以连接 Tailcat 中继和模型服务。

```sh
cd linux-amd64
sha256sum -c SHA256SUMS
./install-server.sh /home/me/projects/example
loginctl enable-linger "$(id -un)"
systemctl --user status miya.service
```

安装器写入 `~/.local/bin/miya` 和用户级 `miya.service`，捕获安装时的 PATH，以便找到 Agent CLI。启用 linger 后，用户退出 SSH 登录也能继续运行；部分系统需要管理员启用该选项。需要配置代理、CLI 环境变量时，用 `systemctl --user edit miya.service` 添加 `[Service]` 下的 `Environment=`，再安排服务重启。

默认服务数据目录为 `${XDG_CONFIG_HOME:-~/.config}/Miya`。通过 `MIYA_DIR` 自定义时，后续管理命令也要使用同一个值。服务锁、Socket、配对身份、连接码、运行信息和记忆保存在该目录；既有 Agent 配置、项目列表、会话和自动化继续使用运行用户的原有存储位置，备份时也应保留其 HOME 下的 Miya / Codex / Claude 数据。

不使用 systemd 时可以前台运行：

```sh
./miya -serve -data-dir "$HOME/.config/Miya" -workspace /home/me/projects/example
```

`-serve` 会持续运行，Tailcat 初始化失败时自动重试。原有 `-server` 仍是由本机窗口管理、空闲后退出的后台模式。

## 连接

服务日志只报告连接码的文件位置，不记录连接码本身。初始化完成后，在服务器查看：

```sh
~/.local/bin/miya -connection
```

电脑端在侧栏底部、设置右侧打开工作空间菜单，选择「添加远程工作空间…」，填写名称（例如 `prod-us`）并粘贴 `miya://connect?...` 连接码。验证连接后立即进入该工作空间，无需重启应用。

层级为「工作空间 → 项目 → 对话」。「本机」和保存过的服务器并列显示在工作空间菜单中，点选即可切换；每个工作空间保留自己的当前对话、草稿、附件和文件／终端面板。连接新工作空间期间仍可使用原工作空间，失败或取消时继续留在原处。重启应用会恢复上次选择的工作空间，断线自动重连；原来设置页保存的单个服务器连接会自动迁移。

也可从命令行保存并选择一个远程工作空间：

```sh
/path/to/Miya -connect 'miya://connect?...'
```

手机端在现有添加桌面页面粘贴同一个连接码，无需新的配对协议。电脑端也可以在「设置 → 手机」显示服务器二维码，供手机扫描。

服务器连接码同时允许完整电脑端操作，等同于该服务器运行用户的操作权限，请只交给可信设备；文件使用 0600 权限保存。电脑端本地保存的连接设置也使用 0600 权限。

## 操作与范围

- 电脑端继续使用现有会话界面，发送、停止、审批、任务、排队、会话历史、模型设置、记忆和自动化复用后台服务协议。
- 添加项目时填写服务器绝对路径，「浏览」列出服务器文件夹。文件面板、编辑保存、`@` 文件索引及 Git 审阅访问服务器，不访问电脑上的同名路径。
- 终端在服务器项目目录启动登录 Shell；输入、输出、终端尺寸经独立 Tailcat 流传输，关闭终端或所属连接失效时结束 Shell。任务是否需要长期运行应由 Agent / 自动化管理，不依赖窗口中的交互终端。
- 电脑选择的附件上传到服务器（一次最多 6 个、单个 10 MB、合计 20 MB），历史图片预览从服务器读取。服务器端重试草稿保留其附件身份。
- 浏览器中 `http://localhost:3000`、`127.0.0.1` 和 IPv6 回环指向服务器的 HTTP 服务，支持路径、查询参数和 WebSocket；其他网站由电脑正常打开。运行面板的 Web 服务也使用同一条私有通道。服务器 localhost HTTPS 预览暂不支持。
- 用量页和菜单栏读取服务器上的 Agent 账号；账号与重置卡操作也交给服务器处理。
- Linux 无界面服务器不提供 macOS 录屏、窗口接管、Face ID 或本机系统授权。手机会按现有能力标记隐藏这些入口；需要图形桌面的动作由服务器上的相应环境提供，不能当作 Mac 桌面来操作。

这是一位服务器用户的共享实例。各客户端共享项目、账号和设置；不包含多租户隔离或独立设备权限管理。

## 更新与排查

重新运行新包的安装器。旧二进制备份为 `~/.local/bin/miya.previous`，替换文件时保留旧进程。已运行的服务会收到空闲重启请求，等待运行中的 Agent、自动化相关工作和交互终端结束后退出，由 systemd 启动新版。客户端自动重连。电脑端连接远程服务器不会依据本机可执行文件路径自动替换或强制停止它。

也可从电脑端远程升级：进入远程工作空间，打开「设置 → 通用 → 当前工作空间的 Miya」，查看自动检查的结果并点击「升级对端」。连接和检查更新不会自动安装。服务端从项目官方 GitHub Release 下载适合自身架构的更新包，校验 SHA-256、大小、包内版本、程序身份与架构后备份替换，等待所有运行任务和交互终端结束再退出。systemd 实例由 systemd 拉起，直接运行的 `-serve` 实例在清理完成后启动替代进程；客户端自动重连，核对目标版本和 PID 后才显示升级成功。客户端断开不会取消已接受的安装。

首次启用需要在对端安装包含远程升级接口的版本；旧版服务不会因新客户端连接而获得升级接口。当前支持 macOS Miya.app 和 Linux amd64 / arm64 服务端，只提供与当前服务协议兼容的更新。发布者须先发布带有 `miya-update.json` 和对应更新压缩包的正式 Release；草稿和预发布不会被默认更新地址选中。该流程不需要 SSH，也不会接收客户端指定的下载 URL 或安装命令。

更新包校验值来自 HTTPS 上的官方发布清单，信任边界是项目 GitHub Release 的发布权限；它不是独立的离线签名机制。对端必须能访问 GitHub 及其 Release 文件下载域名，并拥有安装目录的写权限。远程升级保留带时间戳的 `miya.previous-*` 备份，不覆盖安装器已有的 `miya.previous`。安装失败会保留原文件；新版已安装而尚未退出旧服务时，界面明确显示等待切换。

```sh
systemctl --user status miya.service
journalctl --user -u miya.service -n 100 --no-pager
~/.local/bin/miya -restart-when-idle
```

回滚时将 `miya.previous` 复制为临时文件后重命名覆盖 `miya`，再运行 `-restart-when-idle`。`systemctl --user stop miya.service` 是主动停止整个服务，会结束该实例的运行进程。

## 验证

```sh
go test ./...
go test -race ./tests/integration ./internal/remote -run 'TestServer|TestRemoteHost|TestRemoteBrowser|TestServiceRestartWhenIdle'
MIYA_SERVER_E2E=1 go test ./tests/integration -run TestServerTailcatEndToEnd -count=1 -timeout=180s
```

端到端测试需要联网，使用临时配对身份和合成项目，不连接生产 Miya 后台。真实服务器安装、真实手机网络及系统登录环境须分别验收，构建成功不代表已安装或远端已切换。

2026-11-04 验证：完整回归 890 项通过；含真实 Tailcat 中继的关键路径竞态检查 16 项通过，覆盖耗时请求期间同连接心跳仍可响应。macOS 电脑端和 iOS 模拟器构建通过，Linux amd64 / arm64 安装包均已生成。Linux arm64 临时容器无需 GTK 或显示服务即可启动；本机客户端经 Tailcat 完成该容器的文件读写、下载、PTY 命令和手机协议连接。安装器的 unit 生成及文件权限经过隔离检查；未在生产服务器上安装，未验证真实 systemd 用户管理器或真实手机弱网。
