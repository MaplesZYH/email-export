# Mail Forwarder

轻量邮件分发转发器——从公共邮箱拉取新邮件，按规则自动转发给目标收件人。

支持两种收件人来源：

- **static**：配置文件静态指定收件人（独立使用）
- **database**：从 PostgreSQL 动态查询收件人（衔接 [WhaleHire Talent Bank](https://github.com/your-org/talent-bank) 等业务系统）

## 工作流程

```text
公共邮箱收到新邮件（如 BOSS 简历）
    ↓
转发器拉取新邮件（IMAP）
    ↓
获取目标收件人列表（static 规则 / PG 查询）
    ↓
按收件人逐个转发（SMTP）
    ↓
记录状态，去重防重发
```

首次运行自动初始化邮箱游标，**不转发历史邮件**，避免洪泛。

## 快速开始

### 编译

```bash
go build -o mail-forwarder ./cmd
```

### 最简运行

```bash
cp config.yaml.example config.yaml
# 编辑 config.yaml，填入 IMAP/SMTP 配置和收件人

# 先 dry-run 试跑
mail-forwarder --config config.yaml --dry-run

# 确认无误后真实发送
mail-forwarder --config config.yaml
```

## 配置文件

### 完整配置项

```yaml
source_mailbox:
  host: imap.example.com
  port: 993
  username: ""
  password: ""                   # 建议通过环境变量注入
  use_ssl: true
  folder: INBOX

smtp:
  host: smtp.example.com
  port: 465
  username: ""
  password: ""                   # 建议通过环境变量注入
  use_ssl: true
  start_tls: false
  from: ""
  from_name: 简历分发助手
  subject_prefix: "[简历转发]"

# talent-bank PG 连接（recipients_source: database 时生效）
database:
  host: ""
  port: 5432
  name: talentbank
  user: ""
  password: ""                   # 建议通过环境变量注入
  ssl_mode: disable

# 运行模式
daemon:
  enabled: false                 # true = 长驻循环, false = 单次执行
  sync_interval: 5m
  recipients_source: static      # "static" | "database"

# static 模式收件人规则
rules:
  - name: default
    enabled: true
    recipients:
      - user-a@example.com
      - user-b@example.com

forward:
  dry_run: true                  # true = 只打印不发送（安全默认值）
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

### 环境变量

所有配置项均可通过环境变量覆盖，前缀 `MAIL_FORWARDER_`，层级用 `_` 连接：

| 配置项 | 环境变量 |
|--------|---------|
| `source_mailbox.password` | `MAIL_FORWARDER_SOURCE_MAILBOX_PASSWORD` |
| `smtp.password` | `MAIL_FORWARDER_SMTP_PASSWORD` |
| `database.host` | `MAIL_FORWARDER_DATABASE_HOST` |
| `database.password` | `MAIL_FORWARDER_DATABASE_PASSWORD` |
| `daemon.enabled` | `MAIL_FORWARDER_DAEMON_ENABLED` |

敏感信息（密码）推荐通过环境变量注入，不写进配置文件。

## 两种收件人来源

### static 模式（默认）

收件人写死在配置文件的 `rules` 中，适合独立使用或测试：

```yaml
daemon:
  recipients_source: static

rules:
  - name: HR组
    enabled: true
    recipients:
      - hr-a@company.com
      - hr-b@company.com
  - name: 技术组
    enabled: true
    recipients:
      - tech@company.com
```

`enabled: false` 的规则会被跳过，多个规则中的重复收件人自动去重。

### database 模式（衔接业务系统）

从 PostgreSQL 的 `resume_mailbox_settings` 表动态查询 `status = 'enabled'` 的 `email_address` 作为收件人：

```yaml
daemon:
  recipients_source: database

database:
  host: your-pg-host
  port: 5432
  name: talentbank
  user: forwarder_readonly
  password: ""
  ssl_mode: disable
```

查询 SQL：

```sql
SELECT email_address
FROM resume_mailbox_settings
WHERE status = 'enabled' AND deleted_at IS NULL
```

**数据库只需只读权限**，建账号方式：

```sql
CREATE USER forwarder_readonly WITH PASSWORD 'your-password';
GRANT CONNECT ON DATABASE talentbank TO forwarder_readonly;
GRANT SELECT ON resume_mailbox_settings TO forwarder_readonly;
```

每次同步前实时查询，用户在业务系统中启用/禁用邮箱，转发器自动感知。

## CLI 参数

```
mail-forwarder [选项]

选项:
  --config string     配置文件路径 (默认 "config.yaml")
  --dry-run           只打印转发计划，不真实发送
  --rules-file string 动态规则文件路径
  --recipient string  目标邮箱（可重复传入）
```

优先级：`--recipient` > `--rules-file` > config `rules`

示例：

```bash
# CLI 直接指定收件人（覆盖配置）
mail-forwarder --config config.yaml \
  --recipient user-a@example.com \
  --recipient user-b@example.com

# 使用外部规则文件
mail-forwarder --config config.yaml --rules-file rules.yaml

# dry-run 试跑
mail-forwarder --config config.yaml --dry-run
```

规则文件格式：

```yaml
# 简写
recipients:
  - user-a@example.com

# 完整
rules:
  - name: default
    enabled: true
    recipients:
      - user-a@example.com
```

## Daemon 模式

开启后长驻运行，定时循环同步：

```yaml
daemon:
  enabled: true
  sync_interval: 5m
  recipients_source: database
```

行为：

- 启动后立即执行一次同步
- 之后按 `sync_interval` 间隔循环
- 支持 `SIGINT` / `SIGTERM` 优雅退出
- 每次同步前重新获取收件人列表（database 模式下用户变更实时生效）

## Docker 部署

### 构建镜像

```bash
docker build -t mail-forwarder .
```

### docker-compose

```yaml
services:
  mail-forwarder:
    build: .
    volumes:
      - ./config.yaml:/etc/mail-forwarder/config.yaml:ro
      - forwarder-state:/data
    environment:
      MAIL_FORWARDER_DATABASE_HOST: ${DB_HOST}
      MAIL_FORWARDER_DATABASE_PASSWORD: ${DB_PASSWORD}
      MAIL_FORWARDER_SOURCE_MAILBOX_PASSWORD: ${IMAP_PASSWORD}
      MAIL_FORWARDER_SMTP_PASSWORD: ${SMTP_PASSWORD}
    restart: unless-stopped

volumes:
  forwarder-state:
```

创建 `.env` 文件（不进 git）：

```bash
DB_HOST=your-pg-host
DB_PASSWORD=your-db-password
IMAP_PASSWORD=your-imap-password
SMTP_PASSWORD=your-smtp-password
```

启动：

```bash
docker compose up -d

# 查看日志
docker compose logs -f

# 停止
docker compose down
```

## 多环境配置

项目提供三个配置文件模板，按场景选择：

| 文件 | 场景 | 收件人 | dry-run | daemon |
|------|------|--------|---------|--------|
| `config.local.yaml` | 本地 CLI 测试 | static | ✅ | ❌ |
| `config.db-local.yaml` | 本地衔接 PG | database | ✅ | ❌ |
| `config.yaml` | 生产 Docker 部署 | database | ❌ | ✅ |

使用方式：

```bash
# 本地独立测试
go run ./cmd --config config.local.yaml

# 本地衔接 PG 测试
go run ./cmd --config config.db-local.yaml

# 生产部署
docker compose up -d
```

所有含敏感信息的配置文件和 `.env` 均已加入 `.gitignore`。

## 项目结构

```text
cmd/
  main.go                     入口：CLI 解析、daemon 循环、收件人来源选择
internal/
  config/                     配置加载、校验、规则解析
  mailbox/                    IMAP 连接、邮件拉取、附件解析
  forwarder/                  转发编排、MIME 构建、SMTP 发送、去重
  recipient/                  收件人来源接口与实现
    recipient.go              Source 接口定义
    static.go                 static 模式（从配置读取）
    database.go               database 模式（从 PG 查询）
  state/                      JSON 状态文件（游标、去重、投递记录）
  logging/                    Zap 日志初始化
```

## 去重机制

同一封邮件不会重复转发给同一收件人。

去重键计算：

```text
SHA256(source_username | message_id_or_uid | attachment_hashes | recipient_email)
```

状态文件 `forwarder_state.json` 记录每条投递的去重键和状态（success / failed），原子写入（先写临时文件再 rename）。

## 安全原则

- 配置模板不含真实密码
- 日志不打印密码、授权码
- `dry_run` 默认为 `true`
- 数据库只需只读权限
- 状态文件权限 `0600`

## 错误处理

| 场景 | 行为 |
|------|------|
| 源邮箱连接失败 | 本次退出，不推进游标 |
| SMTP 发送失败 | 记录失败，不影响其他收件人 |
| 数据库连接失败 | daemon 模式下记日志，下次循环重试 |
| 单次执行全部失败 | 进程退出码非 0 |
| 状态文件写入失败 | 进程退出码非 0，避免丢失去重记录 |

## License

MIT
