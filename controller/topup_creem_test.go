package controller

import "testing"

func TestVerifyCreemSignature_RejectsEmptySecret(t *testing.T) {
	if verifyCreemSignature("payload", "anything", "") {
		t.Fatalf("verifyCreemSignature must return false when secret is empty (test mode bypass removed)")
	}
}

func TestVerifyCreemSignature_RejectsBadSignature(t *testing.T) {
	if verifyCreemSignature("payload", "bad", "secret") {
		t.Fatalf("verifyCreemSignature must reject incorrect signature")
	}
}

func TestVerifyCreemSignature_AcceptsValidSignature(t *testing.T) {
	secret := "test_secret"
	payload := "{\"event\":\"checkout.completed\"}"
	sig := generateCreemSignature(payload, secret)
	if !verifyCreemSignature(payload, sig, secret) {
		t.Fatalf("verifyCreemSignature must accept signature generated with the same secret")
	}
}
