package store

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/bson"
	"math"
	"testing"
	"time"
)

func TestPlanPriceValidationAndSerialization(t *testing.T) {
	p := PlanInput{Name: "Paid", SKULimit: 10, WarehouseLimit: 1, BillingInterval: "monthly"}
	for _, fee := range []float64{-1, math.NaN(), math.Inf(1), 1e9 + 1} {
		p.Price = &fee
		if validatePlan(p) == nil {
			t.Fatal("invalid price", fee)
		}
	}
	fee := 499.99
	p.Price = &fee
	if err := validatePlan(p); err != nil {
		t.Fatal(err)
	}
	plan := Plan{PlanInput: p, Price: 499.99, Currency: "INR"}
	j, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var got Plan
	if err = json.Unmarshal(j, &got); err != nil || got.Price != 499.99 {
		t.Fatal(string(j), got, err)
	}
	b, err := bson.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = bson.Unmarshal(b, &got); err != nil || got.Price != 499.99 {
		t.Fatal(got, err)
	}
}
func TestPaidAccessBoundariesAndCoverage(t *testing.T) {
	now := time.Date(2026, 1, 31, 10, 0, 0, 0, billingZone).UTC()
	trial := now.AddDate(0, 0, 14)
	sub := Subscription{Plan: Plan{PlanInput: PlanInput{BillingInterval: "monthly"}, Price: 500}, StartedAt: now, PeriodAnchor: trial, TrialEndsAt: &trial, AutoRenew: true}
	account := BusinessAccount{Base: Base{IsActive: true}, Subscription: &sub}
	if a := subscriptionAccess(account, trial.Add(-time.Millisecond)); !a.HasAccess || a.Status != "trialing" {
		t.Fatal(a)
	}
	if a := subscriptionAccess(account, trial); a.HasAccess || a.Status != "paymentRequired" || a.Billing.AmountDue != 500 {
		t.Fatal(a)
	}
	start, end, anchor := subscriptionCoverage(sub, now)
	if !start.Equal(trial) || !end.Equal(billingBoundary(trial, 1)) || !anchor.Equal(trial) {
		t.Fatal(start, end, anchor)
	}
	sub.PaidFrom = &start
	sub.PaidUntil = &end
	if a := subscriptionAccess(account, trial); !a.HasAccess || a.Status != "active" {
		t.Fatal(a)
	}
	if a := subscriptionAccess(account, end); a.HasAccess || a.Status != "paymentRequired" {
		t.Fatal(a)
	}
	late := end.AddDate(0, 0, 10)
	s, e, a := subscriptionCoverage(sub, late)
	if !s.Equal(late) || !a.Equal(late) || !e.Equal(billingBoundary(late, 1)) {
		t.Fatal(s, e, a)
	}
	sub.AutoRenew = false
	sub.AccessEndsAt = &end
	if a := subscriptionAccess(account, end); a.HasAccess || a.Status != "expired" {
		t.Fatal(a)
	}
	account.IsActive = false
	if a := subscriptionAccess(account, trial); a.HasAccess || a.Status != "businessInactive" {
		t.Fatal(a)
	}
	// Early renewals retain the original day even after a short month.
	sub.TrialEndsAt = nil
	sub.PeriodAnchor = now
	feb := billingBoundary(now, 1)
	sub.PaidUntil = &feb
	s, e, a = subscriptionCoverage(sub, now)
	if !s.Equal(feb) || e.In(billingZone).Day() != 31 || !a.Equal(now) {
		t.Fatal(s, e, a)
	}
	leap := time.Date(2024, 2, 29, 12, 0, 0, 0, billingZone)
	sub.Plan.BillingInterval = "yearly"
	sub.PeriodAnchor = leap
	next := billingBoundary(leap, 12)
	sub.PaidUntil = &next
	_, e, _ = subscriptionCoverage(sub, leap)
	if e.In(billingZone).Day() != 28 || e.In(billingZone).Year() != 2026 {
		t.Fatal(e)
	}
}
func TestSubscriptionQuoteSnapshotsAndCouponCents(t *testing.T) {
	sub := Subscription{SubscriptionID: "SUB-1", Plan: Plan{Price: 100.01}}
	for _, tc := range []struct {
		discount Discount
		want     float64
	}{{Discount{Type: "percentage", Value: 10}, 10}, {Discount{Type: "fixed", Value: 1000}, 100.01}, {Discount{Type: "percentage", Value: 100}, 100.01}} {
		sub.CurrentBill = &SubscriptionBill{SubscriptionID: "SUB-1", BillID: "SBI-1", Status: "pending", Subtotal: 100.01, Discount: &tc.discount, DiscountAmount: money(discountCents(&tc.discount, 10001, 10001))}
		b := sub.CurrentBill
		b.Total = money(cents(b.Subtotal) - cents(b.DiscountAmount))
		if q := subscriptionQuote(sub); q.DiscountAmount != tc.want || q.AmountDue != b.Total {
			t.Fatal(q)
		}
	}
	sub.CurrentBill.Status = "paid"
	if q := subscriptionQuote(sub); q.Discount != nil || q.AmountDue != 100.01 || q.BillID != "" {
		t.Fatal("coupon carried to next cycle", q)
	}
}
func TestSubscriptionReceiptValidation(t *testing.T) {
	in := SubscriptionPaymentInput{BillID: "SBI-1", RequestID: "payment-request-123", Amount: 10, Method: "cash", Note: "Cash received"}
	if err := validateSubscriptionPayment(in); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*SubscriptionPaymentInput){func(p *SubscriptionPaymentInput) { p.Amount = -1 }, func(p *SubscriptionPaymentInput) { p.Amount = math.NaN() }, func(p *SubscriptionPaymentInput) { p.Note = " " }, func(p *SubscriptionPaymentInput) { p.RequestID = "short" }, func(p *SubscriptionPaymentInput) { p.Method = "upi" }, func(p *SubscriptionPaymentInput) { p.Method = "card" }} {
		p := in
		change(&p)
		if validateSubscriptionPayment(p) == nil {
			t.Fatal(p)
		}
	}
	old := SubscriptionPayment{AccountID: "ACC-000001", BillID: in.BillID, Amount: 10, Method: "cash", Note: in.Note}
	if !sameSubscriptionPayment(old, "ACC-000001", in) || sameSubscriptionPayment(old, "ACC-000002", in) {
		t.Fatal("idempotency scope")
	}
	in.Amount = 11
	if sameSubscriptionPayment(old, "ACC-000001", in) {
		t.Fatal("changed receipt matched")
	}
}

func TestPlanTermsChangeRequiresExplicitAcceptance(t *testing.T) {
	p := Plan{PlanID: "PLN-1", PlanInput: PlanInput{Name: "Starter", SKULimit: 10, BillingInterval: "monthly"}, Currency: "INR"}
	q := p
	if !samePlanTerms(p, q) {
		t.Fatal("identical terms differ")
	}
	q.Price = 500
	if samePlanTerms(p, q) {
		t.Fatal("price change hidden")
	}
	q = p
	q.SKULimit = 20
	if samePlanTerms(p, q) {
		t.Fatal("capacity change hidden")
	}
}
