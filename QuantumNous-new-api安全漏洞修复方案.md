# QuantumNous new-api 安全漏洞修复方案

> 配套文档:`QuantumNous-new-api代码审计报告.md`(共 10 项发现)
> 基线复核:全部漏洞已比对当前代码确认存在(漏洞 9 在生产路径下被外层逻辑拦截,降为硬化项)
> 范围:仅技术修复方案,不含运维/SOP 流程层加固

## 修复优先级与拆分

| 优先级 | 漏洞 | 修复主题 | 拆分建议 |
|---|---|---|---|
| P0 | 1, 2, 4, 5 | 支付回调金额一致性 | 单 PR(共用一套金额校验抽象) |
| P0 | 6 | TokenAuthReadOnly 鉴权一致性 | 独立小 PR |
| P0 | 10 | `/api/setup` 匿名接管 | 独立小 PR |
| P1 | 7 | SMTPS 证书校验 | 独立小 PR |
| P1 | 3 | Creem 邮箱覆盖 | 与漏洞 2 同 PR 或单独 |
| P2 | 8 | Session Cookie Secure | 独立小 PR |
| P2 | 9 | Creem 验签 test mode bypass | 独立小 PR(硬化) |

P0 应在同一发版周期内合并;P1 紧随;P2 可滚动收敛。每个 PR 必须自带回归测试 — 报告中列出的本地 PoC 测试可直接转为 negative test(改为预期 PASS = 漏洞被拦)。

---

## P0-1:漏洞 1 / 2 / 4 / 5 — 支付回调金额强一致性校验

### 修复目标

任何支付 webhook 进入"发货分支"前,必须满足:

1. **币种一致**:回调币种 == 订单创建时记录的币种;
2. **金额不少付**:回调实付金额 ≥ 本地订单期望金额(允许多付以兼容退税/小费等场景,但不允许少付);
3. **方向不可篡改**:外部回调只决定"是否发货",发货数量始终来自本地订单(已经是现状,保留)。

### 涉及文件

| 文件 | 改动内容 |
|---|---|
| `model/topup.go` | `TopUp` 增加 `Currency string` 列(若无),新增 `VerifyPaidAmount` 方法 |
| `model/subscription.go` | `SubscriptionOrder` 增加 `Currency` 列(若无),`CompleteSubscriptionOrder` 入参增加 `paidAmountMinorUnit int64, paidCurrency string` |
| `controller/topup_stripe.go` | `fulfillOrder` 把 `amount_total` / `currency` 传入校验 |
| `controller/topup_creem.go` | `handleCheckoutCompleted` 把 `event.Object.Order.AmountPaid` / `Currency` 传入校验 |
| `controller/topup.go` | `EpayNotify` 用 `verifyInfo.Money` 与本地金额对比 |
| `controller/topup_waffo.go` | `handleWaffoPayment` 提取 `result` 中支付金额字段并比对 |
| `controller/subscription_payment_*.go` | 所有订阅 webhook 同样接入校验 |

### 设计:统一校验抽象

在 `model/topup.go` 增加(非内联到每个 webhook,避免四处复制粘贴):

```go
// PaidAmountInput 描述外部回调声称的实付信息。
// AmountMinorUnit:最小单位整数(美元用美分、人民币用分;零小数位币种 JPY/KRW/IDR/VND 等以单位为整数)。
//                 不同提供商需在控制器里换算到这一单位再传入。
// Currency:大写 ISO 4217 代码,如 "USD" / "CNY" / "JPY"。
type PaidAmountInput struct {
    AmountMinorUnit int64
    Currency        string
}

// 零小数位币种:回调金额已经是整数主单位,不再 ×100。
var zeroDecimalCurrencies = map[string]bool{
    "JPY": true, "KRW": true, "IDR": true, "VND": true,
}

// VerifyPaidAmount 校验回调声称的实付金额是否满足"币种一致 + 不少付"。
// 比较单位:本地订单的 Money 是元/美元(float),需先换算到最小单位再比较。
// 容忍精度:四舍五入后允许 ≤ 1 个最小单位的下浮(覆盖 0.999... 这类浮点误差)。
func (t *TopUp) VerifyPaidAmount(in PaidAmountInput) error {
    if in.Currency == "" {
        return ErrPaymentAmountMissing
    }
    if t.Currency != "" && !strings.EqualFold(t.Currency, in.Currency) {
        return fmt.Errorf("currency mismatch: order=%s callback=%s", t.Currency, in.Currency)
    }
    factor := 100.0
    if zeroDecimalCurrencies[strings.ToUpper(in.Currency)] {
        factor = 1.0
    }
    expected := int64(math.Round(t.Money * factor))
    if in.AmountMinorUnit + 1 < expected {
        return fmt.Errorf("underpayment: expected_minor=%d paid_minor=%d", expected, in.AmountMinorUnit)
    }
    return nil
}
```

`SubscriptionOrder` 加同名方法,期望金额取自 `GetSubscriptionPlanById(order.PlanId).Price`(注意 plan 价格才是权威,`order.Money` 已经是冗余字段)。

### 各控制器接入点

#### Stripe(`controller/topup_stripe.go` `fulfillOrder` L260+)

```go
amountTotal, _ := strconv.ParseInt(event.GetObjectValue("amount_total"), 10, 64) // Stripe 已是最小单位
currency := strings.ToUpper(event.GetObjectValue("currency"))
in := model.PaidAmountInput{AmountMinorUnit: amountTotal, Currency: currency}

// 订阅分支
if err := model.CompleteSubscriptionOrder(referenceId, payloadJson, model.PaymentProviderStripe, "", in); err == nil { ... }

// 充值分支:Recharge 入参追加 in,内部在事务里调 topUp.VerifyPaidAmount(in)
if err := model.Recharge(referenceId, customerId, callerIp, in); err != nil { ... }
```

#### Creem(`controller/topup_creem.go` `handleCheckoutCompleted` L286+)

```go
in := model.PaidAmountInput{
    AmountMinorUnit: int64(event.Object.Order.AmountPaid), // Creem amount_paid 单位是 cents/最小单位(已查官方文档确认)
    Currency:        strings.ToUpper(event.Object.Order.Currency),
}
```

✅ Creem `amount_paid` 单位已确认:**最小单位(cents)**。例如 $12.10 在 webhook 里是 `1210`。来源:[Creem 官方 Webhooks 文档](https://docs.creem.io/skills/creem-api/WEBHOOKS)。

#### Epay(`controller/topup.go` `EpayNotify` L364+)

`verifyInfo.Money` 在 epay 库里通常是字符串元金额,需要:

```go
paidYuan, _ := strconv.ParseFloat(verifyInfo.Money, 64)
in := model.PaidAmountInput{
    AmountMinorUnit: int64(math.Round(paidYuan * 100)),
    Currency:        "CNY", // Epay 主要 CNY,如有多币种需从订单读
}
```

#### Waffo(`controller/topup_waffo.go` `handleWaffoPayment` L378+)

需要扩展 `webhookPayloadWithSubInfo` 解析,把 `result.PaymentNotificationResult` 里的支付金额/币种字段读入。

⚠️ **Waffo SDK 字段名未直接验证**(本仓库未缓存 SDK,网络受限无法 fetch `proxy.golang.org`)。基于以下三点,**强假设** SDK 字段为 `Amount string json:"amount,omitempty"` 与 `UserCurrency string json:"userCurrency,omitempty"`:

1. 当前代码出向请求(`controller/topup_waffo.go:249`)用 `OrderAmount: formatWaffoAmount(payMoney, currency)` — 字符串、主单位(如 `"9.99"`),与零小数币种(JPY/KRW/IDR/VND)兼容;
2. 出向用 `OrderCurrency`,回向以 `userCurrency` 为常见命名(见 Waffo 类支付平台惯例);
3. Web 检索结果建议字段为 `Amount/UserCurrency string`(无权威源,仅作参考)。

**实施前必须做一步验证**:`go mod download github.com/waffo-com/waffo-go && find $(go env GOMODCACHE)/github.com/waffo-com -name "*.go" | xargs grep -l PaymentNotificationResult` ,然后 cat 出真实 struct。如字段名/单位不一致,按真实定义改。

假设字段确认后:

```go
// 主单位字符串 → 最小单位整数
paid, _ := strconv.ParseFloat(result.Amount, 64)
currency := strings.ToUpper(result.UserCurrency)
factor := int64(100)
if zeroDecimalCurrencies[currency] {
    factor = 1
}
in := model.PaidAmountInput{
    AmountMinorUnit: int64(math.Round(paid * float64(factor))),
    Currency:        currency,
}
if err := model.RechargeWaffo(merchantOrderId, c.ClientIP(), in); err != nil { ... }
```

注意:`zeroDecimalCurrencies` 在 `topup_waffo.go:64` 已存在,直接复用。`TopUp.Money` 在 Waffo 出向是用美元/CNY 的 float 存,与本地比对前需要乘对应 factor,**不能假设永远 ×100**。

### `Recharge*` / `CompleteSubscriptionOrder` 调用点改造

在每个发货事务的开头,**FOR UPDATE 锁定订单后立即调用 `VerifyPaidAmount`**:

```go
err = DB.Transaction(func(tx *gorm.DB) error {
    err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", referenceId).First(topUp).Error
    if err != nil {
        return errors.New("充值订单不存在")
    }
    if topUp.PaymentProvider != PaymentProviderXxx { ... }
    if topUp.Status != common.TopUpStatusPending { ... }

    // ↓ 新增金额校验,放在状态校验之后、扣额度之前
    if err := topUp.VerifyPaidAmount(in); err != nil {
        topUp.Status = common.TopUpStatusUnderpaid // 新状态,避免被任何重试再次进入发货
        _ = tx.Save(topUp).Error
        return err
    }
    // ↓ 原有发货逻辑
})
```

新增 `common.TopUpStatusUnderpaid` 常量。少付订单**不退款**只标记,需要财务/客服介入(若未来需要自动退款,在 webhook 验签后调用 provider 退款 API,这是后续功能,不在本次修复范围)。

### 数据库迁移(需兼容 SQLite/MySQL/PostgreSQL,见 CLAUDE.md Rule 2)

```sql
ALTER TABLE topups ADD COLUMN currency VARCHAR(8) NOT NULL DEFAULT '';
ALTER TABLE subscription_orders ADD COLUMN currency VARCHAR(8) NOT NULL DEFAULT '';
```

GORM AutoMigrate 即可(在 `model/main.go` 的 `migrateDB` 流程中)。订单创建路径需补回写 `Currency`(各 RequestPay 函数当前没有写入)。

历史数据 `currency=''`:`VerifyPaidAmount` 中 `t.Currency != ""` 的判断保证不会卡住 legacy 订单 — 但日志层应额外打 WARN,推动前端切换。

### 测试

| 测试 | 期望 |
|---|---|
| Stripe `amount_total = 0.01` × 本地 7 美元订单 | 拒绝发货,订单标 underpaid |
| Stripe `amount_total = 700` (cents) × 本地 7 美元 | 正常发货 |
| Stripe `currency = EUR` × 本地 USD 订单 | 拒绝(币种不一致) |
| Creem / Epay / Waffo 同一组用例 | 行为一致 |
| 历史订单(`Currency=''`) 正常回调 | 不卡 legacy 订单(降级到只比金额) |

把审计报告里的 `TestSecurity_StripeTopupWebhook_UnderpaymentStillCreditsLocalOrder` 等测试**反向**(原本期望 PASS = 漏洞存在,改成期望发货失败 = 漏洞已修),作为回归门禁。

---

## P0-2:漏洞 6 — TokenAuthReadOnly 鉴权一致性

### 修复目标

只读接口和写接口对 token 的"是否有效"判断必须一致。被禁用、过期、耗尽的 token 不应继续读取使用量与日志。

### 涉及文件

`middleware/auth.go` `TokenAuthReadOnly` (L210-274)

### 改动

将其内部从直接调用 `GetTokenByKey` 改为调用与 `TokenAuth` 相同的 `model.ValidateUserToken`,共用 status/expired/quota 三层检查:

```go
func TokenAuthReadOnly() func(c *gin.Context) {
    return func(c *gin.Context) {
        key := c.Request.Header.Get("Authorization")
        // ...原有 Bearer/sk- 前缀剥离逻辑保留...

        token, err := model.ValidateUserToken(key) // ← 替换 GetTokenByKey
        if err != nil {
            c.JSON(http.StatusUnauthorized, gin.H{
                "success": false,
                "message": common.TranslateMessage(c, i18n.MsgTokenInvalid),
            })
            c.Abort()
            return
        }
        // 用户封禁检查保留(ValidateUserToken 不查用户)
        userCache, err := model.GetUserCache(token.UserId)
        // ...
    }
}
```

### 兼容性

- `ValidateUserToken` 在 token 已耗尽时会自动写 `Status = TokenStatusExhausted`,这与只读接口语义存在轻微张力 — 只读访问触发了写。可接受,因为这是真实的状态推进,不是只读接口的副作用累积。
- 如果担心读触发写造成 DB 压力,可加一个 `validateOnly` 形态:`ValidateUserTokenLite(key)`,只判定不写状态。优先采用前者,因为简单。

### 测试

把 `TestSecurity_DisabledTokenReadOnlyEndpoints_StillExposeUsage` 与 `TestSecurity_ExpiredTokenReadOnlyEndpoints_StillExposeLogs` 改为期望 401。

---

## P0-3:漏洞 10 — `/api/setup` 匿名接管 root

### 修复目标

防止任意访客在初始化窗口期抢占 root。修复必须满足:

1. 不依赖前端约定;
2. 不阻塞合法首次部署;
3. 即使 `setup` 表被人为清空也不应可被外部直接重置 root。

### 推荐方案:启动期一次性 setup token

```
启动时:
  if !RootUserExists() && !constant.Setup:
      生成 32 字节随机 token,写到 stdout/log:
        [INFO] First-run setup token: xxxxxx (only shown once, expires in 30min)
      存入内存 + 落盘到 data/setup.token(权限 0600)
  else:
      不生成

PostSetup:
  必须携带 X-Setup-Token header
  与内存 token / 文件 token 比对(constant time compare)
  比对成功后立即清除两份 token
```

### 涉及文件

| 文件 | 改动 |
|---|---|
| `controller/setup.go` | `PostSetup` 增加 token 验证,首步即验证;`GetSetup` 不返回 token |
| `main.go` 或 `model/main.go` 启动钩子 | 启动时按条件生成 token,写日志 + 文件 |
| `common/setup_token.go`(新增) | token 生成、读取、清除工具 |

### 加固:来源限制(可选叠加)

在 `router/api-router.go` L21-22 给 `/api/setup` 包一层中间件:仅放行 `127.0.0.1` / `::1` / 私有网段(`10.*`、`172.16-31.*`、`192.168.*`)。这层只是纵深防御,不能替代 token 校验 — 因为很多用户用 docker run + 直接公网映射,IP 限制会误伤。建议**默认关闭,通过 env 开启**。

### 测试

- 未携带 token 的 `POST /api/setup` → 401/403
- 携带错误 token → 401
- 携带正确 token → 创建 root 成功,token 立即失效
- 再次 POST → 系统已初始化,拒绝

---

## P1-1:漏洞 7 — SMTPS 证书校验

### 修复目标

默认严格校验 TLS 证书。仅在管理员显式选择(自签证书内网部署等场景)时才跳过。

### 涉及文件

`common/email.go` L58-64
`common/global.go` 或 `setting/system_setting/*` 中的 SMTP 配置组
管理后台前端开关(`web/src/...` 中 SMTP 配置面板)

### 改动

```go
// common/global.go
var SMTPSkipTLSVerify = false // 管理后台可配置;默认严格校验

// common/email.go L58-64
if SMTPPort == 465 || SMTPSSLEnabled {
    tlsConfig := &tls.Config{
        InsecureSkipVerify: SMTPSkipTLSVerify, // ← 改为读配置
        ServerName:         SMTPServer,
    }
    // ...
}
```

`controller/misc.go` L288/L312/L318 处理后台 SMTP 测试发信的几个分支同步改造。前端在 SMTP 配置区加一个 "跳过 TLS 证书校验(不推荐)" 复选框,默认未勾选,勾选时给红字警告。

### 注意

升级即变严:**已经在用自签证书 SMTP 服务器的部署会立即发信失败**。需要在 release notes 显著标注:升级后若收不到验证码邮件,先开"跳过 TLS 证书校验"或换证书。

---

## P1-2:漏洞 3 — Creem webhook 覆盖空邮箱

### 修复目标

Webhook 不应直接写用户邮箱字段。

### 选项与建议

**选项 A(推荐,简洁):** 直接删掉 `RechargeCreem` 中写邮箱的逻辑,只保留充值额度。

```go
// model/topup.go RechargeCreem L432-445 整段删除
updateFields := map[string]interface{}{
    "quota": gorm.Expr("quota + ?", quota),
}
err = tx.Model(&User{}).Where("id = ?", topUp.UserId).Updates(updateFields).Error
```

`customerEmail` 参数可保留只用于审计日志(`RecordTopupLog`)。

**选项 B(若产品坚持需要绑定):** 把 `customerEmail` 写入 `topUp.CustomerEmail`(新列),在用户登录态下展示一个"是否将该邮箱设为账户邮箱"的引导,需要用户主动点击同意。

PoC 已证明 A 即可终结此漏洞,推荐 A。如未来产品确需此功能,再走 B。

### 测试

把 `TestSecurity_CreemTopupWebhook_UnderpaymentStillCreditsAndCanSetEmptyEmail` 中"邮箱被覆盖"那段断言改为"邮箱保持为空"。

---

## P2-1:漏洞 8 — Session Cookie Secure

### 修复目标

部署在 HTTPS 后时,session cookie 自动带 Secure。

### 改动(`main.go` L173-179)

两种思路,推荐组合:

1. **自适应**:启动时检测 `system_setting.ServerAddress` 是否以 `https://` 开头,自动设 `Secure: true`;
2. **可覆盖**:加配置 `SessionCookieSecure` (默认 `auto`,可手动 `true`/`false`)。

```go
secure := false
if strings.HasPrefix(strings.ToLower(system_setting.ServerAddress), "https://") {
    secure = true
}
// 允许 env override
if v := os.Getenv("SESSION_COOKIE_SECURE"); v != "" { ... }

store.Options(sessions.Options{
    Path:     "/",
    MaxAge:   2592000,
    HttpOnly: true,
    Secure:   secure,
    SameSite: http.SameSiteStrictMode,
})
```

### 注意

部署在反向代理后端、HTTPS 终止在边界、上游是 HTTP 的场景需要文档说明:此时 `ServerAddress` 应配 `https://...`(用户面向地址),否则 cookie 不会带 Secure,导致 SameSite=Strict + 无 Secure 在 Chrome 等浏览器上的安全行为不如预期。

---

## P2-2:漏洞 9 — Creem 验签 test mode bypass

### 修复目标

代码层面不允许"无 secret 即跳过验签"的分支存在,无论 test mode 与否。

### 改动(`controller/topup_creem.go` L36-44)

直接删除 test mode bypass 分支:

```go
func verifyCreemSignature(payload string, signature string, secret string) bool {
    if secret == "" {
        logger.LogWarn(context.Background(), fmt.Sprintf("Creem webhook secret 未配置 signature=%q", signature))
        return false
    }
    expectedSignature := generateCreemSignature(payload, secret)
    return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
```

外层 `isCreemWebhookEnabled` 已经把空 secret 拦在 webhook 入口之前,删除 bypass 不会破坏任何合法路径。

### 测试

把 `TestSecurity_CreemWebhookSecretBypassInTestMode` 改为期望 `verifyCreemSignature("", "", "")` 返回 `false`。

---

## 跨条目共性约束(必须遵守 CLAUDE.md)

实施每条修复时:

- **JSON**:`common.Marshal` / `common.Unmarshal` / `common.UnmarshalJsonStr`,不直接用 `encoding/json`(Rule 1)
- **DB 兼容**:任何新加列或迁移在 SQLite/MySQL/PostgreSQL 上都要可跑;新增 raw SQL 用 `commonGroupCol`/`commonKeyCol` 与 `common.UsingPostgreSQL` 分支;布尔用 `commonTrueVal`/`commonFalseVal`(Rule 2)
- **前端**:用 `bun run dev` / `bun run build`(Rule 3)
- **不动品牌**:严禁修改任何 `QuantumNous` / `new-api` 的标识、商标、模块路径(Rule 5)
- **Optional 字段**:webhook DTO(`AmountPaid` 等)若是可选字段,使用 `*int64` + `omitempty`,避免 0 被误判为"未提供"(Rule 6)

## 验证流程

每个 PR 合并前必须通过:

1. 当前仓库已存在的 `*_security_regression_test.go` 全绿(并按本文要求改写预期);
2. `go test ./...` 全绿;
3. 关键支付链路(P0-1)的金额校验测试覆盖到:少付/正好/多付/币种不符/精度边界(0.999...)五种;
4. 三种数据库各跑一遍 migration + 回归测试。

## 不在本方案范围

- 自动退款(超付的 0.999... 容忍 / 少付主动退款):需要业务决策;
- 支付通道间一致性(比如 Stripe/Creem 都换成同一个统一 PaymentProvider 接口):重构层面的改造,本次只做漏洞修复,不动架构;
- 多因子认证、敏感操作二次确认、审计日志接入 SIEM:超出本次审计范围;
- AWVS/ZAP 扫描器结论(报告末尾)的额外加固:扫描器没发现东西不代表不需要复检,但本方案不展开。
