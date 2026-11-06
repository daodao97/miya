# MCP 范围与边界

MCP 的协议、stdio 服务、工具目录、工具适配、调用者身份和会话授权统一放在 `internal/mcp/`。配置也位于这个范围内，Agent 通过 `mcp/config` 获取启动参数。原有 `internal/mcpserver`、`internal/mcpproto`、`internal/mcpconfig` 已删除。

```text
internal/mcp/
  server.go                 JSON-RPC、并发请求、取消与观察等待
  stdio.go                  -mcp 进程和后台连接
  *_tools.go                工具目录、MCP 参数及结果适配
  protocol/                 稳定请求/响应、工具结构、RPC 操作与等待策略
  config/                   Agent 的 MCP 启动参数与工具允许策略
  service/                  后台工具路由、调用者校验与领域服务适配
  access/                   应用访问确认和会话期间的内存授权
```

`internal/mcp` 自己注册全部工具。导入该模块即可提供完整的 `initialize` 和 `tools/list`，无需导入 Controller 或创建窗口；读取工具元数据也不连接后台。`Run` 只连接已经运行的服务，不负责启动或升级它。

`mcp/service.Host` 提供工作区快照、引用解析、会话操作、用户确认、调度，以及记忆、自动化和协作领域服务。Controller 的 `mcp_host.go` 只实现这个接入接口，`mcp_access.go` 只桥接本地或远端会话授权。Controller 不再保存 MCP 工具目录、参数解析或工具分派逻辑。

工具名、参数、描述、注册顺序、受控提示、错误码、JSON 字段、RPC 操作名和超时保持兼容。MCP 对协作命令的 `readOnlyHint`、`destructiveHint` 等注解在 MCP 模块组装；协作领域只描述自身命令、参数和可变性，界面与工具共用它的业务校验。记忆工具参数在 `mcp/protocol`，原文证据校验与保存仍属于记忆领域。

调用者识别从 Host 读取会话快照，不采信工具进程声明的工作区。需要严格身份的操作按工具名和完整参数唯一匹配；匹配不到或出现歧义继续拒绝。原生工具封套由 Agent 的消息投影层归一化，MCP 不导入 Codex 或 Claude 的原生客户端。

`mcp/access.Grants` 仅在内存里保存授权，确认必须使用实时审批队列。远端窗口把确认交给远端服务，不生成本地授权。支持的提示类型、会话范围、过期请求处理、模式与字段校验均沿用既有规则；它不替代操作系统权限或通用 MCP 表单确认。

依赖方向是 Controller → MCP 服务 → 领域服务。MCP 不依赖 Controller、appmodel 或任一视图包；记忆、自动化和协作领域不反向依赖 MCP。现有 Agent 接入仍只依赖 MCP 的配置或访问契约。

`TestMCPModuleBoundaries` 检查命名空间与这些依赖约束，并阻止 Controller 重新注册工具。MCP 自身测试在不导入 Controller 的前提下核对完整工具目录、stdio 元数据、严格调用者匹配和远端授权；既有集成测试继续覆盖取消、EOF、跨项目身份、审批、记忆、自动化及协作。

## 本轮验证

- 全量竞态回归：1245 项通过、26 项跳过，没有失败或数据竞争报告。
- `go vet ./...` 通过；MCP 服务、Host、授权和新增契约测试的 `gopls check` 无诊断。
- macOS arm64、iOS arm64 模拟器构建，以及 Linux amd64、Windows amd64 交叉编译通过。
- 本轮没有安装应用或切换运行中的后台；真实模型、远端和设备相关可选测试仍按原有开关跳过。
