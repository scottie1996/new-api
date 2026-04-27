package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	ErrPaymentAmountMissing  = errors.New("payment amount missing")
	ErrPaymentCurrencyMismatch = errors.New("payment currency mismatch")
	ErrPaymentUnderpayment   = errors.New("payment underpayment")
)

// PaidAmountInput describes what a webhook claims the customer actually paid.
//
// AmountMinorUnit is the integer amount in the smallest currency unit
// (cents for USD/EUR, fen for CNY, JPY/KRW/IDR/VND are integers in their major unit).
// Each provider must convert its native field to this canonical form before
// passing it in.
//
// Currency is the uppercase ISO 4217 code (e.g. "USD", "CNY", "JPY").
type PaidAmountInput struct {
	AmountMinorUnit int64
	Currency        string
}

// zeroDecimalCurrencies lists ISO 4217 codes whose smallest unit is the
// integer major unit (no fractional digits).
var zeroDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true,
	"IDR": true, "JPY": true, "KMF": true, "KRW": true,
	"MGA": true, "PYG": true, "RWF": true, "UGX": true,
	"VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

// minorUnitFactor returns 1 for zero-decimal currencies, 100 otherwise.
func minorUnitFactor(currency string) int64 {
	if zeroDecimalCurrencies[strings.ToUpper(currency)] {
		return 1
	}
	return 100
}

// VerifyPaidAmount checks that a webhook-reported payment is sufficient
// to fulfill a local order.
//
// expectedMajorUnit is the local order's expected price in the major unit
// (e.g. 9.99 for $9.99). expectedCurrency may be empty for legacy rows
// that pre-date the currency column; in that case the currency check is
// skipped but the amount check still runs against the input currency's
// minor-unit factor.
//
// A 1-unit downward tolerance is allowed to absorb float rounding
// (e.g. 9.99 -> 998 when callback reports 999).
func VerifyPaidAmount(expectedMajorUnit float64, expectedCurrency string, in PaidAmountInput) error {
	if in.Currency == "" {
		return ErrPaymentAmountMissing
	}
	if expectedCurrency != "" && !strings.EqualFold(expectedCurrency, in.Currency) {
		return fmt.Errorf("%w: order=%s callback=%s", ErrPaymentCurrencyMismatch, expectedCurrency, in.Currency)
	}
	factor := minorUnitFactor(in.Currency)
	expectedMinor := int64(math.Round(expectedMajorUnit * float64(factor)))
	if in.AmountMinorUnit+1 < expectedMinor {
		return fmt.Errorf("%w: expected_minor=%d paid_minor=%d currency=%s", ErrPaymentUnderpayment, expectedMinor, in.AmountMinorUnit, in.Currency)
	}
	return nil
}

// VerifyPaidAmount on TopUp delegates to the package-level helper using
// the order's recorded Money and Currency.
func (t *TopUp) VerifyPaidAmount(in PaidAmountInput) error {
	return VerifyPaidAmount(t.Money, t.Currency, in)
}

// VerifyPaidAmount on SubscriptionOrder uses the plan's PriceAmount and
// Currency rather than order.Money because the plan is the authoritative
// price source — the order copy can drift if a plan is updated mid-flight.
func (o *SubscriptionOrder) VerifyPaidAmountWithPlan(plan *SubscriptionPlan, in PaidAmountInput) error {
	return VerifyPaidAmount(plan.PriceAmount, plan.Currency, in)
}
