# 邮件分发转发器模块

## 定位

这是一个独立的轻量 CLI 工具，只做一件事：

```text
从一个源邮箱读取新邮件
-> 按配置规则找到目标邮箱
-> 通过 SMTP 转发出去
-> 记录结果，避免重复发送
```

它不绑定 BOSS、人才库、用户系统或任何具体业务。后续如果 `talent-bank` 需要，可以把它作为独立工具接入。

## 第一版目标

第一版只实现默认广播分发：

```text
公共邮箱收到新邮件
-> 转发给配置里的所有启用收件人
```

不做岗位匹配、不做关键词匹配、不接数据库、不做 Web 管理后台。

## 技术选型

参考 `whalehire` 现有 Go CLI 工具风格：

- Go
- Viper：读取 YAML 配置和环境变量
- Zap：日志
- IMAP 邮件库：读取源邮箱
- SMTP 标准库或轻量封装：发送邮件
- JSON 状态文件：保存游标、转发记录和去重信息

## CLI 使用方式

第一版只保留一个命令：

```bash
mail-forwarder --config config.yaml
```

执行一次完整流程：

- 读取配置
- 拉取源邮箱新邮件
- 转发给配置里的目标邮箱
- 写入状态文件

首次真实运行会先初始化邮箱游标，不转发历史邮件，避免把公共邮箱里的旧邮件一次性全部发出去。

如果想先试跑，不真实发送：

```bash
mail-forwarder --config config.yaml --dry-run
```

不需要设计多个子命令，先保持简单。

如果目标邮箱需要运行时动态指定，可以不用改 `config.yaml`：

```bash
mail-forwarder --config config.yaml --recipient user-a@example.com --recipient user-b@example.com
```

如果目标邮箱由业务系统动态生成，也可以让业务系统生成一个规则文件：

```bash
mail-forwarder --config config.yaml --rules-file /tmp/mail-forwarder-rules.yaml
```

规则文件可以很简单：

```yaml
recipients:
  - user-a@example.com
  - user-b@example.com
```

也可以使用完整规则结构：

```yaml
rules:
  - name: default
    enabled: true
    recipients:
      - user-a@example.com
      - user-b@example.com
```

优先级：

```text
--recipient > --rules-file > config.yaml 里的 rules
```

## 配置文件设计

```yaml
source_mailbox:
  host: imap.example.com
  port: 993
  username: boss@example.com
  password: change-me
  use_ssl: true
  folder: INBOX

smtp:
  host: smtp.example.com
  port: 465
  username: boss@example.com
  password: change-me
  use_ssl: true
  from: boss@example.com
  from_name: 简历分发助手
  subject_prefix: "[简历转发]"

rules:
  - name: default
    enabled: true
    recipients:
      - user-a@example.com
      - user-b@example.com

forward:
  dry_run: true
  require_attachments: true
  max_messages_per_run: 50
  allowed_attachment_extensions:
    - .pdf
    - .doc
    - .docx

state:
  file: forwarder_state.json

log:
  level: info
  file: ""
```

## 内部代码拆分

第一版目录尽量少：

```text
cmd/
  main.go
internal/
  config/       配置读取
  mailbox/      IMAP 拉取和邮件解析
  forwarder/    转发编排和 SMTP 发信
  state/        JSON 状态文件
```

## 核心流程

```text
读取配置
-> 加载状态文件
-> 连接源邮箱
-> 如果没有状态文件，初始化当前邮箱游标并退出
-> 拉取游标之后的新邮件
-> 解析邮件和附件
-> 根据启用规则得到目标邮箱列表
-> 为每个目标邮箱生成转发任务
-> 判断是否已经发送过
-> dry-run 则只打印
-> 非 dry-run 则 SMTP 发送
-> 写入状态文件
```

## 邮件解析字段

第一版需要解析：

- Message-ID
- UID
- Subject
- From
- Date
- Text/HTML 正文
- Attachments
- 附件文件名
- 附件 hash

这些字段用于展示、去重、转发和问题排查。

## 去重规则

同一封邮件不能反复发给同一个目标邮箱。

建议去重键：

```text
source_mailbox.username + message_id/uid + attachment_hashes + recipient_email
```

如果邮件没有 `Message-ID`，用 IMAP UID 兜底。

## 状态文件内容

状态文件先用 JSON，方便 CLI 独立运行。

需要保存：

- 源邮箱游标
- 已处理邮件
- 已发送记录
- 失败记录
- 重试次数

示例：

```json
{
  "last_uid": 12345,
  "deliveries": {
    "dedup-key": {
      "message_id": "<mail@example.com>",
      "recipient": "user-a@example.com",
      "status": "success",
      "retry_count": 0,
      "updated_at": "2026-06-15T12:00:00Z"
    }
  }
}
```

## 转发策略

第一版采用重新组装邮件方式：

- 发件人：配置里的 SMTP `from`
- 收件人：分发规则里的目标邮箱
- 标题：保留原标题，可加前缀
- 正文：保留原正文，必要时追加转发说明
- 附件：带上原邮件附件

不在邮件里暴露源邮箱密码、IMAP 信息或调试堆栈。

## 错误处理

- 源邮箱连接失败：本次退出，不推进游标。
- SMTP 发送失败：记录失败，下次可重试。
- 单个目标邮箱失败：不影响其他目标邮箱发送。
- 状态文件写入失败：命令返回失败，避免丢失去重记录。

## 安全原则

- 配置模板不能放真实密码。
- 日志不能打印邮箱密码、授权码。
- dry-run 默认开启更安全。
- 真实发送需要显式关闭 dry-run。

## 后续接入 talent-bank 的方式

第一阶段可以作为独立 CLI 使用：

```bash
mail-forwarder --config config.yaml
```

后续如果要接入 `talent-bank`，有两种方式：

1. 保持 CLI 独立运行，由定时任务触发。
2. `talent-bank` 后端每次运行前生成动态规则文件，然后调用 CLI。
3. 抽出核心包，让 `talent-bank` 后端直接调用。

第一版建议先保持 CLI 独立，避免和业务系统耦合。

## 第一版不做什么

- 不做前端页面。
- 不接业务数据库。
- 不做用户权限。
- 不解析简历内容。
- 不做岗位匹配。
- 不做复杂分组条件。
- 不做长期常驻服务。



  go run ./cmd --config config.yaml \
    --recipient user-a@example.com \
    --recipient user-b@example.com \
    --recipient user-c@example.com \
    --dry-run=false








