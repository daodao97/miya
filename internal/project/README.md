# Project scope

- `model` 定义项目身份，不依赖会话或服务实现。
- `registry` 合并外部发现的项目路径与当前项目，并保存项目排序。数据仍写入原来的项目排序文件。
- `files` 负责目录、文本与图片读取、文件索引和项目路径边界检查。
- `git` 负责 review 来源、差异快照及新旧文件内容，不持有界面状态。
- `access` 定义文件访问合同和本机实现。远程实现位于 `service/client.Files`，通过相同合同服务界面。

项目列表的加入、重命名和移除由 registry 处理，控制器只编排信号、会话和缓存更新。Codex 的可信项目读取与登记写入位于 `agent/codex/projects`，由入口的 integrations 组合后提供给 registry；会话历史不再读取供应商配置。文件补全仍由 chat/composer 排序和展示，磁盘遍历归 files。

项目目录与 `service/workspaces` 的本机/远程服务入口是两种模型。项目路径、JSON 字段、排序文件、Git 命令和符号链接边界沿用现有行为。
