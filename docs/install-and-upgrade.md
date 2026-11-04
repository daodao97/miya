# 安装与后台服务升级

本流程供 Codex、Claude Code 和其他维护者使用。用户要求「安装」时，完成本地构建、安装和验证；已经授权的安装不需要再次询问。系统要求的权限提升照常通过工具申请。

## 前后台分别更新

Miya 使用同一个可执行文件运行不同进程：

| 进程 | 职责 | 更新方式 |
| --- | --- | --- |
| `Miya` | 桌面窗口，镜像后台状态 | 替换应用后退出并重开窗口 |
| `Miya -server` | 管理所有项目的 Codex、Claude Code、手机连接和自动化 | 由新窗口连接时检查版本，空闲后切换 |
| `Miya -mcp …` | 给对话提供 Miya 工具 | 由调用它的 agent 管理，不作为窗口或服务手动重启 |

磁盘上的文件更新后，旧进程还在运行旧代码。文件时间、窗口启动时间或 MCP 出现新工具，都不能单独证明后台服务已升级。

新窗口调用 `connectService`（[service_client.go](../service_client.go)），通过 `hello` 比较 `version`、`exe`、`exeTime`：

| 后台状态 | 程序已有的行为 | 安装者的操作 |
| --- | --- | --- |
| 后台不存在 | 启动后台并连接 | 打开已安装的应用 |
| 与新窗口版本相同 | 直接连接 | 核验完成 |
| 旧后台空闲 | 请求 `quit`，等待退出，再启动新版 | 重开窗口，核验新的 `hello` |
| 旧后台忙碌，协议兼容 | 连接旧后台，发送 `quitWhenIdle` | 保留运行中的对话，报告后台延迟切换 |
| 旧后台忙碌，协议不兼容 | 发送 `quitWhenIdle`，窗口等待并重试连接 | 保留旧后台及窗口，报告后台延迟切换 |

`watchIdle` 每 15 秒检查一次，收到 `quitWhenIdle` 后在服务不再忙碌时退出（[service_server.go](../service_server.go)、[service_unix.go](../service_unix.go)）。保持打开的新窗口会检测断开、自动重连并启动新版（[service_mirror.go](../service_mirror.go)）。未保持窗口时，下次打开应用会启动新版。

连接失败或协议不兼容时，支持后台服务的平台只保留等待连接的窗口，不回退启动本地 agent。否则窗口和后台会各自启动 Codex app-server，争用同一个会话写入者。只有显式设置 `MIYA_SERVICE=0` 或平台不支持后台服务时使用独立运行模式。升级前已经回退为独立模式的旧窗口，应在它的任务结束后正常退出并重开。

忙碌状态涵盖后台项目，不能只看当前项目有没有输出。当前执行安装的对话也可能由这个旧后台承载；此时等待它空闲才返回会造成相互等待，应完成安装后直接报告延期切换。

桌面「设置 → 通用 → 后台服务」提供手动重启入口。空闲时请求后台退出，窗口自动重连并启动服务；有任务时先让用户选择「空闲后重启」或「立即重启」。后者会中断所有项目的运行任务，并暂时断开手机连接，只有用户在设置中明确选择后才发送正常退出请求。窗口保留未发送草稿，并在重连后核验服务 PID 已变化才显示重启成功。独立运行模式不提供单独的后台重启。

## 1. 构建并验签

在仓库根目录执行；以下为 Apple Silicon macOS 的本地安装：

```sh
./scripts/build-macos.sh -platform darwin/arm64
/usr/bin/codesign --verify --deep --strict build/darwin-arm64/Miya.app
/usr/bin/shasum -a 256 build/darwin-arm64/Miya.app/Contents/MacOS/Miya
```

macOS 构建使用项目脚本：远程桌面的 ScreenCaptureKit 原生桥接需要 cgo，而 MyGo 直接构建默认关闭 cgo。脚本还将原生编译的最低系统版本保持为 macOS 13，macOS 14 及以上才启用远程桌面。不要以未启用 cgo 的包验收此功能。

有本机开发签名证书时，用 `MIYA_SIGNING_IDENTITY` 或未跟踪的 `.mygo/macos-signing-identity`（单行证书名称或 SHA-1）固定签名身份。同一团队、同一应用标识的稳定签名可避免每次本地重编译都使 TCC 授权失效；从 ad-hoc 迁移时仍需重新授权一次。不要把其他人的证书身份写入共享配置，也不要为了维持权限放宽 designated requirement。没有配置时保持 ad-hoc 构建。

构建需要访问 Go 缓存，安装需要写入 `/Applications`。遇到沙箱拒绝时，为具体构建或安装命令申请权限提升。没有完成构建、验签之前，不替换现有应用。功能变更应已完成对应验证；仅安装同一份已验证源码时无需重复全量测试。

## 2. 读取当前后台状态

通过服务 Unix socket 查询 `hello` 和必要的 `state` 字段。只输出版本、PID、忙碌状态和项目数量，避免把完整对话、草稿或配置写进日志。

下面的命令可在安装前、重开窗口后分别运行：

```sh
python3 - <<'PY'
import datetime
import json
import os
import socket
from pathlib import Path

binary = Path('/Applications/Miya.app/Contents/MacOS/Miya')
if binary.exists():
    print('installed_binary_mtime:', datetime.datetime.fromtimestamp(
        binary.stat().st_mtime, datetime.timezone.utc).isoformat())

# 默认路径；使用 MIYA_DIR 或服务的短 socket 路径时，设置 MIYA_SOCKET 为实际路径。
path = os.environ.get('MIYA_SOCKET', str(
    Path.home() / 'Library/Application Support/Miya/service.sock'))
if not Path(path).exists():
    print('service_socket_missing:', path)
    raise SystemExit(0)

with socket.socket(socket.AF_UNIX) as conn:
    conn.settimeout(5)
    conn.connect(path)
    stream = conn.makefile('rb')
    for request_id, op in enumerate(['hello', 'state'], 1):
        conn.sendall((json.dumps({'id': request_id, 'op': op}) + '\n').encode())
        while True:
            line = stream.readline()
            if not line:
                raise RuntimeError('service disconnected')
            message = json.loads(line)
            if message.get('id') == request_id:
                break
        if message.get('error'):
            raise RuntimeError(message['error'])
        result = message.get('result', {})
        if op == 'hello':
            print('service_hello:', json.dumps(result, ensure_ascii=False))
        else:
            print('service_state:', json.dumps({
                'current_threads': len(result.get('threads', [])),
                'background_projects': len(result.get('projectThreads', {})),
                'busy_projects': result.get('projectBusy', {}),
            }, ensure_ascii=False))
PY
```

自定义数据目录和长路径的 socket 回退算法见 [service_proto.go](../service_proto.go) 中的 `serviceDir`、`serviceSocket`。找不到默认 socket 时先检查实际路径和进程，不能直接认定服务没运行。

## 3. 备份替换并重开窗口

先复制到 `/Applications` 下的临时目录并验签，再将旧版重命名为备份，最后把临时目录重命名为正式应用。这样可回滚，也避免原地覆盖运行中程序的可执行文件。

```sh
python3 - <<'PY'
import datetime
import hashlib
import os
import subprocess
from pathlib import Path

source = Path.cwd() / 'build/darwin-arm64/Miya.app'
target = Path('/Applications/Miya.app')
stamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S-%f')
stage = Path('/Applications/.Miya-install-' + stamp + '.app')
backup = Path('/Applications/.Miya-backup-' + stamp)

subprocess.run(['/usr/bin/ditto', str(source), str(stage)], check=True)
subprocess.run(['/usr/bin/codesign', '--verify', '--deep', '--strict', str(stage)], check=True)
old_moved = False
try:
    if target.exists():
        os.rename(target, backup)
        old_moved = True
    os.rename(stage, target)
except BaseException:
    if old_moved and not target.exists():
        os.rename(backup, target)
    raise

relative_binary = Path('Contents/MacOS/Miya')
expected = hashlib.sha256((source / relative_binary).read_bytes()).hexdigest()
actual = hashlib.sha256((target / relative_binary).read_bytes()).hexdigest()
if actual != expected:
    raise RuntimeError('installed binary hash differs from build; keep backup and investigate')
print('installed:', target)
print('backup:', backup if old_moved else 'none')
print('sha256:', actual)

# 退出的是桌面应用；后台服务独立运行，由新窗口连接时安排更新。
subprocess.run(['/usr/bin/osascript', '-e',
    'tell application "/Applications/Miya.app" to quit'], check=True, timeout=15)
subprocess.run(['/usr/bin/open', '-n', str(target)], check=True)
print('window_reopen_requested')
PY
```

不要执行 `pkill Miya`、`killall Miya`、按名字批量结束进程、强杀 `-server`，也不要删除服务锁或 socket 来绕过单实例保护。后台可能正在执行其他项目、手机端或安装者自己的对话。使用 [service_unix.go](../service_unix.go) 中已有的单实例与升级流程。

## 4. 分别验收安装、窗口和后台

1. 对已安装的 `/Applications/Miya.app` 再验签，确认二进制 SHA-256 与本次构建一致。
2. 检查新窗口进程确实启动，例如读取 `ps -axo pid,lstart,command`，只保留 Miya 窗口和 `-server` 的行。`open` 返回成功只表示打开请求已提交。
3. 再查一次 `hello`。将其 `exe`、`exeTime` 与已安装二进制路径和修改时间比较，注意时区；比较的是后台记录的启动版本，不能只读取磁盘文件。PID 和启动时间作为辅助证据。
4. 后台仍忙碌且版本较旧时，不强制替换。确认新窗口已打开后，说明它会等待空闲自动切换。若还没有实际发生切换，不能报告后台已更新。
5. 如后台已切换，再读取必要状态或检查本次功能，确认服务可连接、窗口能接收数据。保留本次旧版备份，并在安装失败时给出备份位置。

完成报告按实际状态写，例如：

> 已安装并重开窗口，验签及二进制校验通过。后台仍有对话运行，将在结束后自动切换新版；本次依赖后台的功能届时生效。

或：

> 已安装并重开窗口，后台已切换新版，版本与安装文件一致，状态读取正常。
