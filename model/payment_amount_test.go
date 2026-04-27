package model

import (
	"errors"
	"testing"
)

func TestVerifyPaidAmount(t *testing.T) {
	cases := []struct {
		name        string
		expected    float64
		expectedCcy string
		in          PaidAmountInput
		wantErr     error
	}{
		{
			name:        "exact match USD",
			expected:    9.99,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 999, Currency: "USD"},
			wantErr:     nil,
		},
		{
			name:        "overpay USD ok",
			expected:    7.00,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 1000, Currency: "USD"},
			wantErr:     nil,
		},
		{
			name:        "underpay USD rejected",
			expected:    7.00,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 1, Currency: "USD"},
			wantErr:     ErrPaymentUnderpayment,
		},
		{
			name:        "currency mismatch rejected",
			expected:    7.00,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 700, Currency: "EUR"},
			wantErr:     ErrPaymentCurrencyMismatch,
		},
		{
			name:        "missing callback currency rejected",
			expected:    7.00,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 700, Currency: ""},
			wantErr:     ErrPaymentAmountMissing,
		},
		{
			name:        "legacy order without currency still verifies amount",
			expected:    7.00,
			expectedCcy: "",
			in:          PaidAmountInput{AmountMinorUnit: 700, Currency: "USD"},
			wantErr:     nil,
		},
		{
			name:        "legacy order with insufficient amount rejected",
			expected:    7.00,
			expectedCcy: "",
			in:          PaidAmountInput{AmountMinorUnit: 1, Currency: "USD"},
			wantErr:     ErrPaymentUnderpayment,
		},
		{
			name:        "JPY zero-decimal exact match",
			expected:    1000,
			expectedCcy: "JPY",
			in:          PaidAmountInput{AmountMinorUnit: 1000, Currency: "JPY"},
			wantErr:     nil,
		},
		{
			name:        "JPY zero-decimal underpay",
			expected:    1000,
			expectedCcy: "JPY",
			in:          PaidAmountInput{AmountMinorUnit: 10, Currency: "JPY"},
			wantErr:     ErrPaymentUnderpayment,
		},
		{
			name:        "rounding tolerance: 9.99 expected, 998 paid is allowed",
			expected:    9.99,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 998, Currency: "USD"},
			wantErr:     nil,
		},
		{
			name:        "rounding tolerance does not allow 2-unit gap",
			expected:    9.99,
			expectedCcy: "USD",
			in:          PaidAmountInput{AmountMinorUnit: 997, Currency: "USD"},
			wantErr:     ErrPaymentUnderpayment,
		},
		{
			name:        "case-insensitive currency match",
			expected:    1.00,
			expectedCcy: "usd",
			in:          PaidAmountInput{AmountMinorUnit: 100, Currency: "USD"},
			wantErr:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyPaidAmount(tc.expected, tc.expectedCcy, tc.in)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got nil", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTopUpVerifyPaidAmount_LegacyRow(t *testing.T) {
	// Legacy row without Currency must still validate amount.
	tu := &TopUp{Money: 7.00, Currency: ""}
	if err := tu.VerifyPaidAmount(PaidAmountInput{AmountMinorUnit: 700, Currency: "USD"}); err != nil {
		t.Fatalf("legacy row with sufficient amount should pass, got %v", err)
	}
	if err := tu.VerifyPaidAmount(PaidAmountInput{AmountMinorUnit: 1, Currency: "USD"}); err == nil {
		t.Fatalf("legacy row with underpayment should fail")
	}
}

func TestSubscriptionOrderVerifyPaidAmountWithPlan(t *testing.T) {
	plan := &SubscriptionPlan{PriceAmount: 19.99, Currency: "USD"}
	order := &SubscriptionOrder{Money: 0, Currency: ""} // even if order.Money drifted, plan price wins

	if err := order.VerifyPaidAmountWithPlan(plan, PaidAmountInput{AmountMinorUnit: 1999, Currency: "USD"}); err != nil {
		t.Fatalf("plan-priced exact match should pass, got %v", err)
	}
	if err := order.VerifyPaidAmountWithPlan(plan, PaidAmountInput{AmountMinorUnit: 100, Currency: "USD"}); err == nil {
		t.Fatalf("underpay relative to plan price should fail")
	}
}
