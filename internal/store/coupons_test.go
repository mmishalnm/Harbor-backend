package store

import (
	"math"
	"testing"
)

func TestCouponValidation(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		value float64
	}{{"percent", 10}, {"percentage", 101}, {"fixed", 0}, {"fixed", -1}, {"fixed", math.NaN()}, {"fixed", math.Inf(1)}, {"percentage", 0.001}, {"fixed", 1e9 + 1}} {
		if validateDiscount(tc.kind, tc.value) == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	for _, code := range []string{"a", "invalid code", "<script>", ""} {
		if _, err := normalizeCoupon(CouponInput{Name: "Welcome", Code: code, Type: "fixed", Value: 10}); err == nil {
			t.Fatal(code)
		}
	}
	c, err := normalizeCoupon(CouponInput{Name: " Welcome ", Code: " save-10 ", Type: "percentage", Value: 12.345})
	if err != nil || c.Name != "Welcome" || c.Code != "SAVE-10" || c.Value != 12.35 {
		t.Fatal(c, err)
	}
}
func TestDiscountPartialDeliveryAndRounding(t *testing.T) {
	for _, tc := range []struct {
		d                     *Discount
		delivered, full, want int64
	}{
		{nil, 248500, 272500, 0},
		{&Discount{Type: "percentage", Value: 10}, 248500, 272500, 24850},
		{&Discount{Type: "fixed", Value: 100}, 248500, 272500, 9119},
		{&Discount{Type: "fixed", Value: 100}, 272500, 272500, 10000},
		{&Discount{Type: "fixed", Value: 100}, 2000, 5000, 2000}, // price fell; cap before prorating
		{&Discount{Type: "percentage", Value: 100}, 1, 1, 1},
		{&Discount{Type: "percentage", Value: 50}, 1, 1, 1},
		{&Discount{Type: "fixed", Value: 1e9}, 5e10, 1e11, 5e10},
	} {
		if got := discountCents(tc.d, tc.delivered, tc.full); got != tc.want {
			t.Fatalf("%+v got %d", tc, got)
		}
	}
}
func TestDiscountLineAllocationExactCents(t *testing.T) {
	for _, discount := range []int64{0, 1, 2, 100, 24850, 248500} {
		lines := []InvoiceLine{{Amount: 2160}, {Amount: 325}}
		applyInvoiceDiscount(lines, discount)
		var net, allocated int64
		for _, l := range lines {
			if l.Amount < 0 || cents(l.Subtotal) != cents(l.Amount)+cents(l.DiscountAmount) {
				t.Fatal(l)
			}
			net += cents(l.Amount)
			allocated += cents(l.DiscountAmount)
		}
		if net != 248500-discount || allocated != discount {
			t.Fatal(lines)
		}
	}
	lines := []InvoiceLine{{Amount: 0.01}, {Amount: 0.01}, {Amount: 0.01}}
	applyInvoiceDiscount(lines, 2)
	if lines[0].Amount != 0 || lines[1].Amount != 0 || lines[2].Amount != 0.01 {
		t.Fatal(lines)
	}
}
