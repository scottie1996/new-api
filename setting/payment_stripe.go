package setting

var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""
var StripeUnitPrice = 8.0
var StripeMinTopUp = 1
var StripePromotionCodesEnabled = false

// StripeCurrency is the ISO 4217 currency code of the configured Stripe price.
// It is recorded on each TopUp at creation time and checked against the webhook
// callback at fulfillment time, so a callback claiming a cheaper currency
// cannot satisfy a USD-priced order.
var StripeCurrency = "USD"
