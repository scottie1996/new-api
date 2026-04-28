## **1. 高危：Stripe 支付回调金额未做强一致性校验，导致少付多充 / 少付高配（本地已验证）**

**漏洞介绍：**Stripe 成功回调虽然读取了 amount_total，但它只被写入日志和 payload，没有与本地充值单金额或订阅套餐价格做强一致性校验。回调进入成功分支后，充值场景直接调用本地入账逻辑，真正增加的额度取自本地订单 topUp.Money；订阅场景直接调用 CompleteSubscriptionOrder，真正开通的套餐取自本地 SubscriptionOrder.PlanId。

**漏洞危害：**攻击者一旦能够让 Stripe 成功回调进入业务成功分支，就可能以极低实付金额换取高额度充值，或以极低金额激活高价套餐。

**利用条件：**攻击者拥有一笔属于自己的待支付 Stripe 订单；系统接受了一条进入成功分支的 Stripe 支付完成回调；回调中的实际支付金额低于本地订单金额或套餐价格，但未被系统拦截。真实利用仍取决于生产环境 webhook 验签、支付配置和外围链路条件。

**代码位置：**

new-api/controller/topup_stripe.go#L267

new-api/controller/topup_stripe.go#L273

new-api/controller/topup_stripe.go#L281

new-api/controller/subscription_payment_stripe.go#L79

new-api/model/topup.go#L98

new-api/model/topup.go#L132

new-api/model/subscription.go#L508

new-api/model/subscription.go#L543

**验证：**充值场景下，amount_total=1 仍按本地 7 美元订单入账；订阅场景下，amount_total=1 仍可激活高价套餐。本地实际得到的结果是：

```Plain
=== RUN   TestSecurity_StripeTopupWebhook_UnderpaymentStillCreditsLocalOrder
[INFO] Stripe 充值成功 trade_no=ref_security_stripe_topup amount_total=0.01 currency=USD event_type=checkout.session.completed client_ip=127.0.0.1
--- PASS: TestSecurity_StripeTopupWebhook_UnderpaymentStillCreditsLocalOrder

=== RUN   TestSecurity_StripeSubscriptionWebhook_UnderpaymentStillActivatesPlan
[INFO] Stripe 订阅订单处理成功 trade_no=sub_ref_security_stripe event_type=checkout.session.completed client_ip=127.0.0.1
--- PASS: TestSecurity_StripeSubscriptionWebhook_UnderpaymentStillActivatesPlan
```

## **2. 高危：Creem 支付回调金额未做强一致性校验，导致少付多充 / 少付高配（本地已验证）**

**漏洞介绍：**Creem 成功回调进入业务处理后，同样没有将第三方 amount_paid 或回调商品价格，与本地充值单金额或订阅套餐价格做强一致性校验。充值场景直接进入 RechargeCreem，实际加额取自本地订单 topUp.Amount；订阅场景直接调用 CompleteSubscriptionOrder，实际开通套餐取自本地 SubscriptionOrder.PlanId。

**漏洞危害：**攻击者可能以极低支付金额换取高额度充值，或以极低价格开通高配套餐。

**利用条件：**攻击者拥有一笔属于自己的待支付 Creem 订单；系统接受了一条进入成功分支的 Creem 支付完成回调；回调中的 amount_paid 低于本地订单金额或套餐价格，但业务层未进行强一致性拒绝。真实利用依赖 webhook 被系统接受的外围条件。

**代码位置：**

new-api/controller/topup_creem.go#L295

new-api/controller/topup_creem.go#L297

new-api/controller/topup_creem.go#L314

new-api/controller/topup_creem.go#L340

new-api/controller/subscription_payment_creem.go#L84

new-api/controller/subscription_payment_creem.go#L109

new-api/model/topup.go#L381

new-api/model/topup.go#L410

new-api/model/subscription.go#L543

**验证：**充值场景下，amount_paid=1 仍按本地大额充值单入账；订阅场景下，amount_paid=1 仍可激活高价套餐。本地实际得到的结果是：

```Plain
=== RUN   TestSecurity_CreemTopupWebhook_UnderpaymentStillCreditsAndCanSetEmptyEmail
[INFO] Creem 支付完成回调 trade_no=ref_security_creem_topup creem_order_id=order_security_creem amount_paid=1 currency=USD product_name="Underpaid Product" customer_email="attacker@example.com" customer_name="attacker"
[INFO] Creem 充值成功 trade_no=ref_security_creem_topup creem_order_id=order_security_creem quota=999000 money=99.00 client_ip=192.0.2.1
--- PASS: TestSecurity_CreemTopupWebhook_UnderpaymentStillCreditsAndCanSetEmptyEmail

=== RUN   TestSecurity_CreemSubscriptionWebhook_UnderpaymentStillActivatesPlan
[INFO] Creem 订阅订单处理成功 trade_no=sub_ref_security_creem creem_order_id=order_security_creem_sub
--- PASS: TestSecurity_CreemSubscriptionWebhook_UnderpaymentStillActivatesPlan
```

## **3. 高危：Creem 充值回调可覆盖空邮箱（本地已验证）**

**漏洞介绍：**RechargeCreem 在用户当前邮箱为空时，会直接把 webhook 中的 customerEmail 写入用户邮箱字段，没有绑定确认，也没有“只允许可信邮箱来源修改”的二次校验。

**漏洞危害：**攻击者可趁账户邮箱为空时写入自己的邮箱，干扰账户找回、通知接收、后续身份绑定等流程。

**利用条件：**目标账户存在一笔待支付 Creem 充值单；该账户当前邮箱为空；系统接受了一条进入成功分支的 Creem 回调；回调中的 customerEmail 由攻击者可控或可影响。

**代码位置：**

new-api/model/topup.go#L417

new-api/model/topup.go#L424

new-api/model/topup.go#L425

**验证：**同一测试里，在用户邮箱为空的前提下传入 customer_email="attacker@example.com"，最终断言邮箱字段被覆盖。本地实际得到的结果是：

```Plain
=== RUN   TestSecurity_CreemTopupWebhook_UnderpaymentStillCreditsAndCanSetEmptyEmail
--- PASS: TestSecurity_CreemTopupWebhook_UnderpaymentStillCreditsAndCanSetEmptyEmail
```

## **4. 高危：Epay 支付回调金额未做强一致性校验，导致少付多充 / 少付高配（代码审计确认）**

**漏洞介绍：**Epay 成功回调验签通过后，充值场景直接按本地 topUp.Amount 入账，订阅场景直接按本地 SubscriptionOrder.PlanId 开通套餐，未见将第三方实际支付金额与本地订单金额或套餐价格做强一致性校验。

**漏洞危害：**攻击者可能以低价换取高额充值，或低价开通高价套餐。

**利用条件：**攻击者拥有一笔属于自己的待支付 Epay 订单；系统接受了一条验签通过且进入成功分支的 Epay 回调；外部实际支付金额低于本地订单金额或套餐价格，但业务层未做强一致性校验。

**代码位置：**

new-api/controller/topup.go#L352

new-api/controller/topup.go#L372

new-api/controller/topup.go#L388

new-api/controller/topup.go#L400

new-api/controller/subscription_payment_epay.go#L84

new-api/controller/subscription_payment_epay.go#L159

new-api/controller/subscription_payment_epay.go#L208

new-api/model/subscription.go#L508

**验证：**代码审计

## **5. 高危：Waffo 支付回调金额未做强一致性校验，导致少付多充（代码审计确认）**

**漏洞介绍：**Waffo 支付成功后直接进入 RechargeWaffo，而 RechargeWaffo 按本地 topUp.Amount 计算入账额度，没有将外部支付金额和本地订单金额做强一致性校验。

**漏洞危害：**攻击者可能以低价换取高额充值，属于“外部只决定成功与否，本地决定发货额度”的高风险计费设计错误。

**利用条件：**攻击者拥有一笔属于自己的待支付 Waffo 充值单；系统接受了一条进入成功分支的 Waffo 支付成功回调；外部实际支付金额低于本地订单金额，但系统只按成功状态发货，不核对金额。

**代码位置：**

new-api/controller/topup_waffo.go#L372

new-api/controller/topup_waffo.go#L392

new-api/model/topup.go#L460

new-api/model/topup.go#L480

new-api/model/topup.go#L491

**验证：**代码审计

## **6. 高危：已禁用、已过期或已耗尽的 token 仍可访问只读查询接口（本地已验证）**

**漏洞介绍：**TokenAuthReadOnly 明确只检查 token 是否存在，以及所属用户是否被封禁，不检查 token 的 status、expired_time 和 remain_quota。而 /api/usage/token 与 /api/log/token 正是挂在这套“宽松认证”中间件下。

**漏洞危害：**被管理员禁用的 token、已过期 token、已耗尽 token 仍可继续读取自身额度使用情况和关联日志，属于鉴权绕过或失效令牌继续可读。

**利用条件：**攻击者已经掌握某个历史 token 字符串；该 token 所属用户账号本身未被封禁；服务暴露了 /api/usage/token 或 /api/log/token；即使 token 已被禁用、过期或耗尽，仍可读取信息。

**代码位置：**

new-api/middleware/auth.go#L210

new-api/router/api-router.go#L267

new-api/router/api-router.go#L302

new-api/controller/token.go#L118

new-api/controller/log.go#L70

new-api/model/token.go#L188

**验证：**测试一使用 status=disabled 的 token 调用 /api/usage/token，断言未被中间件拦截且能读到使用量。测试二使用已过期 token 调用 /api/log/token，断言未被中间件拦截且能读到日志。本地实际得到的结果是：

```Plain
=== RUN   TestSecurity_DisabledTokenReadOnlyEndpoints_StillExposeUsage
--- PASS: TestSecurity_DisabledTokenReadOnlyEndpoints_StillExposeUsage

=== RUN   TestSecurity_ExpiredTokenReadOnlyEndpoints_StillExposeLogs
--- PASS: TestSecurity_ExpiredTokenReadOnlyEndpoints_StillExposeLogs
```

## **7. 高危：SMTPS 邮件发送禁用了证书校验（代码审计确认）**

**漏洞介绍：**走 465 / SSL 发信分支时，代码硬编码 InsecureSkipVerify: true，这意味着密码重置邮件、邮箱验证码、通知邮件都可能在 TLS 链路上被中间人劫持。

**漏洞危害：**攻击者如果能控制邮件传输路径，可截获验证码、密码重置链接或篡改通知内容。

**利用条件：**系统启用了 SMTPS/465 或 SSL 发行分支；攻击者能够介入 SMTP 传输路径，例如恶意网关、代理、DNS 劫持、中间人网络位置或受控邮件基础设施；服务端未做额外证书钉扎或二次保护。

**代码位置：**

new-api/common/email.go#L58

new-api/common/email.go#L60

new-api/controller/misc.go#L288

new-api/controller/misc.go#L312

new-api/controller/misc.go#L318

**验证：**本轮未做网络 MITM 回放；代码层已直接确认 SMTPS 分支关闭了证书校验。本地实际得到的结果是：

```Plain
tls.Config{
    InsecureSkipVerify: true,
    ServerName: SMTPServer,
}
```

## **8. 中危：Session Cookie 未设置 Secure 属性（代码审计确认）**

**漏洞介绍：**会话初始化时 Secure:false 是硬编码。即使服务最终部署在 HTTPS 后，浏览器收到的 session cookie 也不带 Secure 属性。

**漏洞危害：**在错误代理配置、明文回落、混合内容或运维失误场景下，会话 cookie 更容易被明文暴露或被非 HTTPS 链路携带。

**利用条件：**服务部署在公网，且浏览器存在 HTTP 回落、代理配置错误、边界层明文转发、混合访问或其他非 HTTPS 传输场景；攻击者能够观察或影响该非安全链路。当前 SameSite=Strict 只能减少跨站请求，不解决明文链路携带问题。

**代码位置：**

main.go 

**验证：**本轮未做浏览器抓包回放；代码层已直接确认 cookie 选项缺少 Secure:true。本地实际得到的结果是：

```Plain
store.Options(sessions.Options{
    Path:     "/",
    MaxAge:   2592000,
    HttpOnly: true,
    Secure:   false,
    SameSite: http.SameSiteStrictMode,
})
```

**附：硬化问题，不计入上面主漏洞数**

## **9. 低危：Creem 在 test mode 且 secret 为空时可跳过验签（本地已验证，但当前受外围开关限制）**

**漏洞介绍：**verifyCreemSignature 在 CreemWebhookSecret="" 且 CreemTestMode=true 时会直接返回 true。不过当前 webhook 是否启用还会被外层 isCreemWebhookEnabled() 限制，而该限制要求 secret 非空，所以这条目前更像硬化缺陷或死代码风险。

**漏洞危害：**当前代码下，这条问题单独不会直接形成主可利用漏洞；但一旦后续重构、回归或外围启用条件发生变化，可能转化为真实验签绕过点。

**利用条件：**当前版本下需要外围启用条件同时被放松、绕过或回归退化，才可能转化为真实可利用问题；单独依赖这一点不足以直接打通 webhook。

**代码位置：**

new-api/controller/topup_creem.go#L36

new-api/controller/payment_webhook_availability.go#L31

new-api/controller/payment_webhook_availability.go#L35

new-api/controller/security_regression_test.go#L347

**验证：**本地测试直接调用验签函数，确认在 test mode 下空 secret 会返回 true。本地实际得到的结果是：

```Plain
=== RUN   TestSecurity_CreemWebhookSecretBypassInTestMode
--- PASS: TestSecurity_CreemWebhookSecretBypassInTestMode
```

## **10. 低危：未初始化实例可被匿名访客直接接管 root（本地已验证）**

**漏洞介绍：**/api/setup 对外公开，PostSetup 只要发现系统尚未初始化、数据库里还没有 root 用户，就会直接创建 root 账号并写入 setup 记录，没有预共享密钥、安装令牌、来源限制或一次性初始化保护。

**漏洞危害：**任意能访问该服务的人，都可以在首次部署、空库恢复、误删 setup 记录等场景下抢先完成初始化并获取最高权限。

**利用条件：**攻击者能够访问服务暴露的 /api/setup；目标实例处于未初始化状态，或数据库中尚无 root 用户；管理员尚未先一步完成初始化。

**代码位置：**

new-api/router/api-router.go#L20

new-api/router/api-router.go#L21

new-api/controller/setup.go#L54

new-api/controller/setup.go#L65

new-api/controller/setup.go#L122

new-api/controller/setup.go#L156

new-api/controller/setup.go#L162

验证：测试中未登录直接调用 PostSetup，随后断言成功创建 role=100 用户，并成功写入 setup 记录。本地实际得到的结果是：

```Plain
=== RUN   TestSecurity_PreInitSetup_AllowsAnonymousRootCreation
--- PASS: TestSecurity_PreInitSetup_AllowsAnonymousRootCreation
```

## **AWVS、ZAP等自动化工具未扫描到严重漏洞，无注入、XSS等OWASP TOP 10漏洞**