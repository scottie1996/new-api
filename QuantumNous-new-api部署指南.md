# QuantumNous new-api 部署指南(security-fixes 分支)

本文档说明如何编译并部署 `security-fixes` 分支镜像 — 用于替换上游 `calciumion/new-api:latest`。该分支修复了审计报告里 10 个安全漏洞(详见 `QuantumNous-new-api代码审计报告.md` 与 `QuantumNous-new-api安全漏洞修复方案.md`)。

> 上游镜像不包含本仓库的安全修复。如要应用,必须用本分支自构镜像。

---

## 1. 编译

仓库自带两个 Dockerfile:

| 文件 | 何时用 |
|---|---|
| `Dockerfile` | 标准多阶段(bun + go),推荐 ≥ 16 GB 内存的构建机 |
| `Dockerfile.prebuilt` | 内存受限(Docker Desktop ≤ 8 GB)时使用,跳过容器内前端构建,需要先在宿主机编译前端 |

### 1.1 标准构建(BuildKit,推荐)

```bash
git clone -b security-fixes git@github.com:scottie1996/new-api.git
cd new-api
docker build -t new-api:security-fixes .
```

### 1.2 低内存构建(Docker Desktop 用此路径)

容器内的 Vite production 构建在 8 GB Docker VM 上会被 OOM kill。绕过办法:在宿主机用 bun 先把两个前端构建好,再用 `Dockerfile.prebuilt` 只跑 Go 阶段。

> 自 upstream v1.0 起,前端拆成两个:`web/default`(React 19 + Rsbuild + Radix UI,默认主题)与 `web/classic`(React 18 + Vite + Semi UI,经典主题)。后端通过 `//go:embed` 同时嵌入两份产物,运行时按主题切换 — 都必须存在,否则 `go build` 会报 `pattern web/.../dist: no matching files found`。

```bash
# 准备宿主机 bun(macOS / Linux)
curl -fsSL https://bun.sh/install | bash
export PATH="$HOME/.bun/bin:$PATH"

# 在仓库根目录
cd new-api

# 1) 构建 classic(Vite,内存峰值约 4-6 GB)
cd web/classic
bun install --frozen-lockfile
DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION=$(cat ../../VERSION) bun run build
cd ../..

# 2) 构建 default(Rsbuild,内存峰值约 3-5 GB)
cd web/default
bun install --frozen-lockfile
bun run build
cd ../..

# 3) 用 Go-only Dockerfile 构建后端(此时 web/{default,classic}/dist 都已就绪)
DOCKER_BUILDKIT=0 docker build -f Dockerfile.prebuilt -t new-api:security-fixes .
```

构建完成后:

```bash
docker images new-api:security-fixes
# REPOSITORY   TAG              IMAGE ID       SIZE
# new-api      security-fixes   ...            ~163MB
```

### 1.3 推到私有镜像仓库(可选)

如果是多机部署,推到私仓再 pull 一次:

```bash
docker tag new-api:security-fixes registry.example.com/your-team/new-api:security-fixes
docker push registry.example.com/your-team/new-api:security-fixes
```

---

## 2. 部署(替换 docker-compose 中的镜像)

把现有 compose 里的 `image: calciumion/new-api:latest` 换成本地 tag 即可。

```yaml
services:
  # 1. 数据库
  db:
    image: postgres:15-alpine
    container_name: ai-db
    restart: always
    environment:
      POSTGRES_USER: admin
      POSTGRES_PASSWORD: bitfuFU20260304
      POSTGRES_DB: ai_platform
    volumes:
      - ./postgres/data:/var/lib/postgresql/data
    networks:
      - langbot_network

  # 2. 模型中转 — 替换为 security-fixes 镜像
  new-api:
    image: new-api:security-fixes        # ← 改这一行(原 calciumion/new-api:latest)
    container_name: new-api
    restart: always
    ports:
      - "3000:3000"
    environment:
      - SQL_DSN=postgres://admin:bitfuFU20260304@db:5432/ai_platform?sslmode=disable
      - TZ=Asia/Shanghai
      # 安全相关推荐配置 ↓
      - SESSION_COOKIE_SECURE=true       # 部署在 HTTPS 后建议显式开
      - SETUP_TOKEN_FILE=/data/setup_token  # 一次性 setup token 落盘位置
    volumes:
      - ./new-api/data:/data             # 持久化 sqlite 缓存与 setup_token
    depends_on:
      - db
    networks:
      - langbot_network

networks:
  langbot_network:
    driver: bridge
```

启动:

```bash
docker compose up -d
docker compose logs -f new-api
```

---

## 3. 首次启动:setup token 流程(漏洞 10 修复带来的新步骤)

⚠️ **新行为**:数据库里**不存在 root 用户**(全新部署 / 数据库被清空)时,容器启动会自动生成一次性 setup token,日志里以高亮形式打印:

```
[SYS] 2026/04/28 - 05:22:07 | system is not initialized and no root user exists
[SYS] 2026/04/28 - 05:22:07 | ============================================================
[SYS] 2026/04/28 - 05:22:07 | FIRST-RUN SETUP TOKEN — required for POST /api/setup:
[SYS] 2026/04/28 - 05:22:07 | X-Setup-Token: d9f8d54971e25c6e5b58b539ff926eec6227da18f9690a8eebc26849c01dc510
[SYS] 2026/04/28 - 05:22:07 | file: setup_token
[SYS] 2026/04/28 - 05:22:07 | Token is single-use; it self-destructs once setup succeeds.
[SYS] 2026/04/28 - 05:22:07 | ============================================================
```

**完成初始化的两种方式:**

### 方式 A:通过前端管理后台

1. 浏览器打开 `http://your-host:3000/setup`
2. 在 **X-Setup-Token** 字段粘入日志里的 token
3. 填管理员账号密码,提交

### 方式 B:命令行 curl

```bash
# 取容器里的 token
TOKEN=$(docker exec new-api cat /data/setup_token)

# 提交 setup
curl -X POST http://localhost:3000/api/setup \
  -H "Content-Type: application/json" \
  -H "X-Setup-Token: $TOKEN" \
  -d '{
    "username": "admin",
    "password": "your-strong-password",
    "confirmPassword": "your-strong-password",
    "SelfUseModeEnabled": false,
    "DemoSiteEnabled": false
  }'
# {"success":true,"message":"系统初始化成功"}
```

成功后 token 立即作废,后续 `/api/setup` 会被拒绝。

> **如果是从已有 root 用户的数据库升级**(比如 docker compose 复用了旧的 postgres 卷),启动日志里**不会**出现这段 token 提示 — 系统认为已初始化,跳过 token 流程。这是预期行为。

---

## 4. 升级路径(从 calciumion/new-api:latest)

| 项 | 行为 |
|---|---|
| GORM 自动迁移 | 启动时给 `topups` / `subscription_orders` 加 `currency` 列(SQLite/MySQL/PostgreSQL 均兼容) |
| 旧订单 `currency=''` | 金额校验自动降级为只比金额、跳过币种检查(兼容现存数据) |
| Setup token | 已有 root 用户时不触发,无侵入 |
| Cookie Secure | 默认按 `ServerAddress` 是否 https 自动判断;可用 `SESSION_COOKIE_SECURE=true/false` 强制 |
| SMTPS 证书校验 | 默认严格(改变!)。已有自签证书 SMTP 服务器的部署需在管理后台开 `SMTPSkipTLSVerify`,否则发不出邮件 |
| Creem `customer_email` 覆盖空邮箱 | 直接禁用,webhook 不再写 user.Email(漏洞 3 修复) |

**升级前必看**:如果你正使用 SMTPS(465 端口或开启 SSLEnabled)且 SMTP 证书是自签的,**升级后会发不出验证码邮件**。两种处理:

1. 换正式证书(推荐)
2. 在管理后台 → 系统设置 → SMTP 设置里勾选"跳过 TLS 证书校验"

---

## 5. 新增配置项一览

### 5.1 环境变量(在 docker-compose 的 `environment` 段)

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SETUP_TOKEN_FILE` | `setup_token` | 一次性安装 token 落盘位置;多机/挂卷部署建议指到持久化卷 |
| `SESSION_COOKIE_SECURE` | (空,自适应) | `true`/`false` 强制 cookie 的 Secure 属性 |

### 5.2 数据库 OptionMap(在管理后台 → 系统设置)

| Key | 默认 | 说明 |
|---|---|---|
| `SMTPSkipTLSVerify` | `false` | 仅在 SMTP 用自签证书时开 |
| `StripeCurrency` | `USD` | Stripe 价格币种,用于 webhook 校验时与回调对账 |
| `EpayCurrency` | `CNY` | Epay 回调币种(Epay 协议本身不带币种字段) |

(SubscriptionPlan 的 Currency 字段早已存在,本分支未改动该接口)

---

## 6. 验证修复在线生效

### 6.1 查看 setup token 已激活

```bash
docker compose logs new-api | grep -A2 "FIRST-RUN SETUP TOKEN"
```

### 6.2 探测 /api/status

```bash
curl -s http://localhost:3000/api/status | jq '.success'
# true
```

### 6.3 跑容器内单元测试(可选)

```bash
docker run --rm -v $(pwd):/build -w /build -e CGO_ENABLED=0 \
  golang:1.26.1-alpine \
  go test ./model/... ./common/... ./controller/...
# ok  github.com/QuantumNous/new-api/model    1.139s
# ok  github.com/QuantumNous/new-api/common   1.313s
# ok  github.com/QuantumNous/new-api/controller 0.164s
```

### 6.4 触发金额校验(支付沙箱场景)

构造一个 paid_minor < expected 的 webhook 回调,应得到:
- `topups.status = 'underpaid'`(本分支新增状态)
- 用户额度**未**增加
- 日志中 `payment` 关键字出现 `expected_minor=... paid_minor=... currency=...`

---

## 7. 常见问题

### 7.1 镜像太大?

163 MB 已经是 debian-bookworm-slim + 静态 Go 二进制 + 嵌入前端资产的合理体积。如果要更小,可以基于 `gcr.io/distroless/base-debian12` 自定义最终阶段(留作未来优化)。

### 7.2 Docker 构建 OOM?

容器内 Vite 至少需要 ~6 GB 可用堆。Docker Desktop 默认 8 GB 全用容易触发。两个解法:

1. 调大 Docker Desktop 内存到 12 GB(`Settings → Resources → Memory`)
2. 用 `Dockerfile.prebuilt` + 宿主机 bun(见 §1.2)

### 7.3 Setup 时拿不到 token?

- `docker compose logs new-api | grep "X-Setup-Token"` 找不到 → 数据库已有 root 用户,不需要 token,直接登录即可
- 找得到 token 行但 `POST /api/setup` 报 `setup token 校验失败` → 复制时漏字符,重新 docker exec 取一次

### 7.4 旧前端缓存?

升级后浏览器可能缓存了旧前端 JS。`Cmd/Ctrl + Shift + R` 强刷,或后端的 vite chunks 文件名带 hash 也会自动失效。

---

## 8. 回滚

如果要回到上游版本:

```yaml
new-api:
  image: calciumion/new-api:latest   # 改回这一行
```

数据库层面,`currency` 列保留也不会影响上游版本读写(GORM 只读已知字段)。`underpaid` 状态的订单上游版本看不见也不会处理,需要人工清理或忽略。

---

## 9. 关联文档

- 漏洞描述与攻击复现:`QuantumNous-new-api代码审计报告.md`
- 修复方案与代码定位:`QuantumNous-new-api安全漏洞修复方案.md`
- 提交历史:`git log --oneline main..security-fixes`
