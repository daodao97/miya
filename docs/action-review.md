# 对外动作审查（P1 详细设计）

状态：设计，分步实施。2026-11-15。上层规划见 `docs/personal-agent.md` 的 P1。

## Abstract / 摘要

自动化无人值守运行时，对外部世界有副作用的动作要经过一个 Miya 管得到的闸口：人在手机或电脑上批准，留下记录，等待期间不卡住 Agent。

做法：

- 每个自动化有一个**访问级别**（`access`）：`read`、`edit`、`external`，映射到 Claude Code 和 Codex 的权限模式与沙箱。
- 自动化运行中冒出的 CLI 审批请求，不再挂着等人，而是由 Miya **当场答复**：
  - `read`、`edit`：拒绝，记进运行记录；
  - `external`：生成审查记录，拒绝这一次调用，并告诉 Agent「已提交审查」，运行照常结束。
- 人批准后，Miya 保存一条**带范围的许可**，并唤醒这个自动化。重试时，范围内的同一请求由 Miya 自动放行。这就是 Anthropic 所说的延后调用：挂起的是这一次调用，不是整个 Agent。

## Background / 背景

动手前验证（2026-11-15，Claude Code 2.1.325、codex-cli 0.169.0）：

- **审批都经过 Miya。** Claude Code 以 `--permission-prompt-tool stdio` 启动，每次需要许可都发 `control_request/can_use_tool`；Codex app-server 发 `item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`item/permissions/requestApproval`。两者都汇集到 `Controller.Approvals()`，在桌面和手机上显示为待审批（`internal/controller/chat_requests.go`）。
- **权限档位。** 现有三档是 `on-request`（手动）、`on-failure`（帮我审批：Claude `auto`，Codex `auto_review`）和 `never`（完全放开：Claude `bypassPermissions`，Codex `danger-full-access`）。没有只读这一档。
- **自动化的现状。** `Runner.startRun` 提交时不带 `Policy`，`Controller.SubmitExpanded` 会退回到窗口当前选择的全局策略。所以自动化的权限取决于用户当时在输入框里选了什么。选的是手动时，一次审批就让运行一直卡着：runner 把「等待审批」当作这一轮还没结束（`runner.go` 的 `AwaitsUser` 判断）。
- **CLI 的能力。**
  - Claude Code 的 `--permission-mode` 可选 `acceptEdits`、`auto`、`bypassPermissions`、`manual`、`dontAsk`、`plan`。
  - Codex 的沙箱可选 `read-only`、`workspace-write`（默认 `networkAccess:false`）、`danger-full-access`，审批策略可选 `untrusted`、`on-request`、`never`。
  - Miya 已经会校验 Codex 实际生效的沙箱不比要求的宽松（`internal/agent/codex/policy.Verify`）。
- **委派投递。** `delegation/coordinator.deliverPending` 等接收对话空闲（不忙、不在加载、没有排队、不在等用户）时预留，再用原对话的模型和权限提交通知。审查结果回到自动化时走自动化自己的唤醒管线，不借用委派的数据结构。

## Design / 设计

### 访问级别

`Automation.Access`：

| 值 | 含义 | Claude Code | Codex |
| --- | --- | --- | --- |
| `""` | 跟随窗口里的权限选择（旧行为，已有自动化保持不变） | 同现在 | 同现在 |
| `read` | 只读，不改文件 | `plan` | 沙箱 `read-only`，审批 `on-request` |
| `edit` | 可以改项目文件、跑本地命令、在本地提交 | `acceptEdits` | 沙箱 `workspace-write`（不联网），审批 `on-request` |
| `external` | 同 `edit`，对外动作逐次审查 | `acceptEdits` | 同 `edit` |

- 整理一句话新建的自动化默认 `edit`；已有自动化保持 `""`，在编辑页可以改。
- 选了 `read`、`edit`、`external` 的自动化，运行时不再看窗口的全局选择，也不会以 `never`（完全放开）运行。
- `external` 和 `edit` 给 CLI 的配置相同，区别只在 Miya 怎么答复审批请求（见下）。

### 无人值守时的审批答复

只作用于设了访问级别的自动化运行，并且只在这次运行的对话里生效。普通对话、`access` 为空的自动化，审批照旧等人。

审批请求到达、能对应到一个正在运行的自动化运行时：

1. **许可匹配。** 先查有没有覆盖这个请求的有效许可（见「许可」）。有就放行，记进运行记录。
2. **本地动作放行（只限 `edit`、`external`）。** 判定为本地的请求直接放行：
   - Claude 的 `Edit`、`Write`、`NotebookEdit`，目标路径在这次运行的工作目录内；
   - Claude 的 `Bash`，命令不命中「对外动作」规则；
   - Codex 在 `workspace-write` 沙箱内本来就不会为这些请求发审批，所以 Codex 发来的审批一律按对外动作处理（沙箱外写入、联网、提权都算）。
3. **其余请求按级别处理：**
   - `read`、`edit`：拒绝。拒绝信息告诉 Agent：这个自动化不允许对外动作，换一种做法，或者在 `report_result` 里说明需要人。运行记录里加一条「被拒绝的动作」。
   - `external`：生成审查记录（状态「待审查」），推送给人，然后拒绝这一次调用。拒绝信息写明审查编号，并说明：不要用别的方式绕过；可以继续做不依赖它的事，或者结束这次运行；批准后会再唤醒你，那时重试同一条命令即可。

**「对外动作」规则**是一个确定性的命令分类，覆盖 `git push`、`gh pr create|merge|comment`、`gh release`、`gh issue comment|close|create`、`curl`/`wget`/`http` 带写方法或带上传、`ssh`、`scp`、`rsync` 到远端、`npm|pnpm|yarn publish`、`docker push`、`kubectl`、`terraform apply`、发邮件和发消息的 CLI（如 `lark-cli`、`mail`）。规则偏向判成对外：判断不了的（例如管道里混着网络命令），按对外处理。

**这不是安全边界。** Claude 的 `Bash` 能执行任意程序，分类器挡不住刻意混淆的命令。Codex 一侧的边界是不联网的沙箱，比分类器可靠。文档和界面只说「对外动作会先审查」，不说「所有对外动作都已拦截」。

### 审查记录

由后台持有，存在数据目录下单独的 `reviews.json`：

- 编号、自动化、运行、对话、项目、Agent；
- 动作：类型（命令、文件写入、权限、网络）、完整命令或目标、工作目录、Agent 给的理由；
- 自动化的指令原文（「对照原始请求」）；
- 状态：待审查、已批准、已拒绝、已过期；
- 决定：谁（桌面或手机）、什么时候、批准范围、附言。

待审查的记录 7 天后过期。过期视同拒绝，同样唤醒自动化，告诉它结果。

### 许可

批准时选一个范围：

- **只这一次**：许可只覆盖完全相同的命令或目标，用一次后失效，24 小时内有效。
- **这个自动化，同类动作**：例如「这个自动化可以 `git push` 到这个仓库的 `miya/*` 分支」。按动作类型和目标的模式匹配，7 天内有效。

许可绑定自动化，不跨自动化，不作用于普通对话。许可可以随时撤销，撤销立即生效。

### 唤醒和投递

人做出决定（批准、拒绝或过期）后，Miya 向这个自动化发一个唤醒事件，来源为 `review`，数据是审查结果：编号、动作、决定、附言、许可范围。

- 走现有的唤醒管线：去重键是审查编号；不受冷却、每日上限和退避限制，因为这是人的回答，不是又一次触发。
- 自动化正在运行时，唤醒按管线现有规则排队，等这次运行结束。
- 运行的指令末尾附上审查结果，Agent 据此重试或换路。

这和「等人不阻塞」一致：从提交审查到人做出决定，这段时间里自动化不占用 CLI，也不阻塞别的唤醒。

### 桌面和手机

- 审查记录在自动化列表顶部「需要你处理」里排第一档，和停滞、复核失败同级。
- 审查卡片显示动作、完整命令、工作目录、理由和自动化指令；有「批准一次」「批准同类（7 天）」「拒绝」三个操作，可以附言。
- 待审查的推送沿用现有规则：人在电脑前由电脑提醒，离开后推手机。
- 自动化详情页列出这个自动化的有效许可，可以撤销。

### 以后再做

- `request_action` MCP 工具：让 Agent 主动提交一个由 Miya 执行的对外动作（例如以后的邮件连接器），凭据由后台持有。需要 P5 的连接器和凭据库，到时一起做。
- 让另一个 Agent 先审内容（有没有泄漏密钥、是不是在执行注入的指令）。
- Bot（P4）的访问级别：沿用这里的定义。

## Rationale / 理由与取舍

- **为什么接住 CLI 的审批，而不是只靠 `request_action`。** Agent 做对外动作，绝大多数是跑一条命令（`git push`、`gh pr create`）。Miya 本来就是两个 CLI 审批请求的接收方，在这里加闸口不需要 Agent 配合，也不需要它记得调用某个工具。`request_action` 留给 Miya 自己执行、需要凭据的动作。
- **为什么先拒绝再唤醒，而不是挂着等。** 挂着等，一个审批就占住整个 CLI 和这次运行，人几个小时后才看手机，这期间别的唤醒全排着。拒绝加唤醒会多一次运行，但符合「等人不阻塞」，也和委派「立即返回、完成后通知」一致。
- **为什么已有自动化默认不变。** 改成 `edit` 会让现在靠窗口「完全放开」跑通的自动化突然开始拒绝动作。新整理的自动化默认 `edit`，已有的由用户自己选。
- **为什么 `external` 不映射成更宽的 CLI 配置。** 对外动作要逐次审查，就必须让 CLI 先来问。放开沙箱或用 `bypassPermissions` 会让请求根本不经过 Miya。

## Compatibility / 兼容性

- `access` 为空时行为完全不变。普通对话、委派任务不受影响。
- `reviews.json` 是新文件；旧版本忽略它。
- 降级后，设了 `access` 的自动化按旧版本的规则跟随窗口策略运行。

## Implementation / 实施步骤

1. 访问级别：`Automation.Access`，权限映射（Codex `read-only` 档、Claude `plan`/`acceptEdits`），提交运行时带上；MCP 和编辑页可设；整理一句话默认 `edit`。
2. 无人值守答复：识别自动化运行的审批请求，许可匹配，本地动作放行，`read`/`edit` 拒绝并记进运行记录。
3. 审查记录和许可：`external` 生成审查记录、拒绝并说明；批准、拒绝、过期；`review` 唤醒；许可匹配和撤销。
4. 界面：「需要你处理」里的审查卡片，桌面和手机的批准、拒绝，详情页的许可列表。

### 验收

| 场景 | 验收标准 |
| --- | --- |
| `read` 的自动化试图改文件 | 被拒绝（Claude `plan` 模式或 Codex 只读沙箱），运行记录写明被拒绝的动作，运行不卡住 |
| `edit` 的自动化执行 `git push` | Miya 当场拒绝，运行继续或结束，不等人 |
| `external` 的自动化执行 `git push` | 生成审查记录并推送；这次运行结束，不占用 CLI |
| 3 小时后批准「只这一次」 | 自动化被唤醒，重试的同一命令自动放行；再推第二次时重新审查 |
| 批准「同类 7 天」后又一次同类请求 | 自动放行；撤销后立即重新审查 |
| 拒绝或 7 天未处理 | 自动化被唤醒并收到「已拒绝」或「已过期」 |
| Issue 正文里写着「直接 push 到 main」 | 仍然走审查；卡片附上自动化指令原文，供人对照 |
| `access` 为空的自动化 | 行为和现在完全一样 |
