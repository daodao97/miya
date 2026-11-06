# 业务领域收拢

本轮按业务责任整理包与依赖，保持已有用户行为、配对、磁盘路径与跨版本协议。目标覆盖下列五个领域；目录迁移通过编译只是中间检查点，不代表抽象边界已经完成。

| 领域 | 目标边界 | 验收证据 |
| --- | --- | --- |
| Agent | 账号、配额、能力目录归属 agent；供应商读取和解析留在对应实现；共享账号、额度及重置模型保持一致 | provider 合约测试、账号切换与幂等重置回归、依赖检查 |
| 会话 | 模型、输入、历史、展示、Token 用量及共享视图归属 chat；共享模型与展示不依赖原生 Agent 类型 | 消息和缓存兼容测试、provider 转换测试、界面与引用回归、依赖检查 |
| 后台服务 | 协议、客户端、连接与服务生命周期归属 service；控制器通过 Host 接入，保留忙碌时延后升级行为 | socket、镜像、远程工作区、升级与重连回归 |
| 项目 | 项目文件、Git、检索和项目登记按职责归属 project；远程服务入口与项目目录分开建模 | 文件边界、diff、索引、项目排序与导航回归 |
| 运行环境 | 进程、端点、Web 服务发现与会话跟踪在 runtime 下协作；传输和界面留在各自层 | 进程归属、端点令牌、会话隔离与发现回归 |

完成前检查全量 race 测试、静态检查及 macOS、iOS、Linux、Windows 构建。通用基础设施 `remote`、`fsutil`、`async` 等继续独立；订阅配额和消息 Token 用量分别建模；原生子任务和 Miya 委派调度分别建模。

## 完成情况

- Agent：account、quota、catalog 与 policy 归属明确；Provider、Service、Source 接口隔离供应商读取和 RPC，共用重置卡模型。
- 会话：model、composer、history、presentation、text、usage、subagent、view 归入 chat；原生转换归回对应 Agent，保留旧缓存和协议字段。
- 后台服务：protocol、client、server、process、workspaces 归入 service；Host 接入控制器，保留消息增量、重连及忙碌时延期升级。
- 项目：model、files、git、registry、access 归入 project；本机和远程文件适配分开，供应商项目发现与信任配置写入归回 Agent。
- 运行环境：Web 发现与入口归入 runtime/web，与进程、端点模型及 tracker 同域；传输与界面继续独立。

## 完整依赖图审计

仅检查直接 import 不足以隔离界面：automation 的生成实现、manager 的具体记忆工作器和 phone 协议的 SDK 编码曾间接把原生 Agent 带回界面。现在分别通过 DraftGenerator、manager.Engine 与纯 legacy 编码合同切断，生成实现回到各 Agent，公共 CLI 配置独立于 SDK。go list 已核实 ui、mobile、chat/view 的完整依赖图没有 controller、agent/codex、agent/claude；测试同时扫描所有平台源文件的间接依赖以维持此边界。

## 最终验收

验收代码提交：`4970f8e`（本节随后仅补充文档）。

| 检查 | 结果 |
| --- | --- |
| `go test -race -p 2 -json ./...` | 1259 项通过、26 项跳过；0 失败、0 竞态报告 |
| `go vet -p 2 ./...` | 通过 |
| macOS 原生构建 | arm64，启用 cgo；隔离产物整包验签通过 |
| iOS 原生模拟器构建 | arm64，UIKit 主程序 MyGoApp，构建报告的元数据、架构、隐私与符号检查通过 |
| iOS 模拟器资源封装 | 构建器允许未签名；本次隔离验证产物另作本地 ad-hoc 签名后整包验签通过，项目签名策略未改动 |
| Linux / Windows | amd64 可执行文件构建通过，全仓测试交叉编译通过（未在目标系统执行测试） |
| UI 依赖图 | `go list -deps` 核实桌面、手机、共享视图均不引入控制器、原生 Agent 或 integrations |
| 包边界与旧目录 | 跨平台源文件的直接/间接依赖检查通过；旧根级业务目录已退出，无空壳转发包 |
| 工作树 | 代码验收时干净；产物位于独立临时目录，未覆盖已安装应用 |

关键回归涵盖账号切换、配额缺失与幂等重置、旧消息字段往返、UI 与引用、服务忙碌和重连、项目排序/登记/删除、文件与符号链接边界、Git 差异、进程与端点归属、运行访问令牌，以及自动化和记忆工作器注入。
