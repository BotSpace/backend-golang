package template

import (
	"encoding/json"
	"testing"
)

// These tests target the pure money/validation helpers that back the
// usecase layer (Price/commission split/rounding/env validation). The full
// Repository interface here is large (catalog + submissions + orders +
// withdrawals), so - unlike the smaller user/project modules - a hand-rolled
// fake repo would mostly be boilerplate; the business-critical logic worth
// locking down with tests lives in these framework-free helpers instead.

func TestComputePriceNoDiscount(t *testing.T) {
	if got := computePrice("100000.00", "0", DiscountTypePercent); got != "100000.00" {
		t.Fatalf("expected 100000.00, got %s", got)
	}
	if got := computePrice("100000.00", "0.00", DiscountTypeAmount); got != "100000.00" {
		t.Fatalf("expected 100000.00, got %s", got)
	}
}

func TestComputePricePercentDiscount(t *testing.T) {
	// 100000 - 10% = 90000.00 (mirrors TemplateModel.price for discount_type=percent).
	got := computePrice("100000.00", "10", DiscountTypePercent)
	if got != "90000.00" {
		t.Fatalf("expected 90000.00, got %s", got)
	}
}

func TestComputePriceAmountDiscount(t *testing.T) {
	got := computePrice("100000.00", "15000", DiscountTypeAmount)
	if got != "85000.00" {
		t.Fatalf("expected 85000.00, got %s", got)
	}
}

func TestComputePriceNeverNegative(t *testing.T) {
	// A discount larger than the price must clamp to 0, not go negative.
	got := computePrice("1000.00", "5000", DiscountTypeAmount)
	if got != "0.00" {
		t.Fatalf("expected 0.00, got %s", got)
	}
}

func TestCommissionShareIs80Percent(t *testing.T) {
	// Mirrors signals/order.py::OrderSignal: author_share = amount * 0.80.
	share, err := commissionShare("100000.00")
	if err != nil {
		t.Fatalf("commissionShare: %v", err)
	}
	if share != "80000.00" {
		t.Fatalf("expected 80000.00 (80%% author commission), got %s", share)
	}
}

func TestCommissionShareOddAmountRoundsToCents(t *testing.T) {
	share, err := commissionShare("33333.33")
	if err != nil {
		t.Fatalf("commissionShare: %v", err)
	}
	// 33333.33 * 0.80 = 26666.664 -> rounded to 2 decimals.
	if share != "26666.66" {
		t.Fatalf("expected 26666.66, got %s", share)
	}
}

func TestDecimalToInt64RoundsHalfAwayFromZero(t *testing.T) {
	cases := map[string]int64{
		"100000.00": 100000,
		"100000.49": 100000,
		"100000.50": 100001,
		"0.00":      0,
	}
	for in, want := range cases {
		got, err := decimalToInt64(in)
		if err != nil {
			t.Fatalf("decimalToInt64(%s): %v", in, err)
		}
		if got != want {
			t.Fatalf("decimalToInt64(%s) = %d, want %d", in, got, want)
		}
	}
}

func TestValidateRequiredEnvsAcceptsWellFormed(t *testing.T) {
	raw := json.RawMessage(`[{"key":"ADMIN_CHAT_ID","description":"chat id"}]`)
	out, err := validateRequiredEnvs(raw)
	if err != nil {
		t.Fatalf("expected valid required_envs, got error: %v", err)
	}
	var items []requiredEnvItem
	if err := json.Unmarshal(out, &items); err != nil || len(items) != 1 {
		t.Fatalf("unexpected normalized output: %s (err=%v)", out, err)
	}
}

func TestValidateRequiredEnvsRejectsReservedKey(t *testing.T) {
	raw := json.RawMessage(`[{"key":"RELEASE_BOT_TOKEN"}]`)
	if _, err := validateRequiredEnvs(raw); err == nil {
		t.Fatal("expected RELEASE_BOT_TOKEN to be rejected as reserved")
	}
}

func TestValidateRequiredEnvsRejectsBadKeyFormat(t *testing.T) {
	raw := json.RawMessage(`[{"key":"admin_chat_id"}]`)
	if _, err := validateRequiredEnvs(raw); err == nil {
		t.Fatal("expected lowercase key to be rejected")
	}
}

func TestValidateRequiredEnvsRejectsDuplicateKeys(t *testing.T) {
	raw := json.RawMessage(`[{"key":"A"},{"key":"A"}]`)
	if _, err := validateRequiredEnvs(raw); err == nil {
		t.Fatal("expected duplicate key to be rejected")
	}
}

func TestValidateCardNumber(t *testing.T) {
	if err := validateCardNumber("8600 1234 5678 9012"); err != nil {
		t.Fatalf("expected valid 16-digit card, got error: %v", err)
	}
	if err := validateCardNumber("123"); err == nil {
		t.Fatal("expected too-short card number to be rejected")
	}
	if err := validateCardNumber("8600-1234-5678-9012"); err == nil {
		t.Fatal("expected non-digit separators to be rejected")
	}
}

func TestSlugify(t *testing.T) {
	if got := slugify("CRM Bot — v2.0!"); got != "crm-bot-v2-0" {
		t.Fatalf("unexpected slug: %s", got)
	}
}

// TestNormalizeFlow mirrors _serialize_template's flow normalisation in
// core/apps/template/views/internal.py: a dict flow passes through, a bare
// list (older flat-node-array format) gets wrapped into {nodes, edges: []},
// and nil/empty falls back to an empty graph — the RAG feed must never send
// the agent a raw array or a missing field where it expects an object.
func TestNormalizeFlow(t *testing.T) {
	cases := []struct {
		name string
		in   json.RawMessage
		want string
	}{
		{"nil", nil, `{"nodes":[],"edges":[]}`},
		{"empty", json.RawMessage(``), `{"nodes":[],"edges":[]}`},
		{"dict passthrough", json.RawMessage(`{"nodes":[{"id":"1"}],"edges":[]}`), `{"nodes":[{"id":"1"}],"edges":[]}`},
		{"bare list wrapped", json.RawMessage(`[{"id":"1"},{"id":"2"}]`), `{"edges":[],"nodes":[{"id":"1"},{"id":"2"}]}`},
		{"whitespace-padded dict", json.RawMessage("  \n{\"nodes\":[]}"), "  \n{\"nodes\":[]}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeFlow(c.in)
			var gotVal, wantVal any
			if err := json.Unmarshal(got, &gotVal); err != nil {
				t.Fatalf("normalizeFlow returned invalid JSON %q: %v", got, err)
			}
			if err := json.Unmarshal([]byte(c.want), &wantVal); err != nil {
				t.Fatalf("bad test fixture: %v", err)
			}
			gotJSON, _ := json.Marshal(gotVal)
			wantJSON, _ := json.Marshal(wantVal)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("normalizeFlow(%s) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}
