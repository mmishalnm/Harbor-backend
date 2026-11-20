package store

import (
	"strings"
	"testing"
	"time"
)

func TestRecurringCalendarBoundaries(t *testing.T) {
	anchor := time.Date(2026, 1, 31, 10, 15, 0, 0, billingZone)
	if got := billingBoundary(anchor, 1).In(billingZone); got.Day() != 28 || got.Month() != 2 || got.Hour() != 10 {
		t.Fatal(got)
	}
	if got := billingBoundary(anchor, 2).In(billingZone); got.Day() != 31 || got.Month() != 3 {
		t.Fatal(got)
	}
	now := time.Date(2031, 3, 1, 10, 0, 0, 0, billingZone)
	start, end := billingPeriod(anchor, now, "monthly")
	if start.In(billingZone).Day() != 28 || start.Month() != 2 || end.In(billingZone).Day() != 31 || end.Year() != 2031 {
		t.Fatal(start, end)
	}
	leap := time.Date(2024, 2, 29, 10, 0, 0, 0, billingZone)
	if billingBoundary(leap, 12).In(billingZone).Day() != 28 || billingBoundary(leap, 48).In(billingZone).Day() != 29 {
		t.Fatal("leap-year renewal drifted")
	}
	start, end = billingPeriod(anchor, billingBoundary(anchor, 2), "monthly")
	if !start.Equal(billingBoundary(anchor, 2)) || !end.Equal(billingBoundary(anchor, 3)) {
		t.Fatal("exact boundary", start, end)
	}
}
func TestSubscriptionTrialRenewalAndCancellation(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	end := now.AddDate(0, 0, 14)
	account := BusinessAccount{Base: Base{IsActive: true}, Subscription: &Subscription{SubscriptionID: "SUB-1", Plan: Plan{PlanInput: PlanInput{BillingInterval: "monthly"}}, StartedAt: now, PeriodAnchor: end, TrialEndsAt: &end, AutoRenew: true}}
	if a := subscriptionAccess(account, now); a.Status != "trialing" || !a.HasAccess || !a.NextRenewalAt.Equal(end) {
		t.Fatal(a)
	}
	if a := subscriptionAccess(account, end); a.Status != "active" || !a.HasAccess {
		t.Fatal(a)
	}
	if a := subscriptionAccess(account, now.AddDate(5, 0, 0)); a.Status != "active" || !a.PeriodEnd.After(now.AddDate(5, 0, 0)) {
		t.Fatal(a)
	}
	account.Subscription.AutoRenew = false
	account.Subscription.AccessEndsAt = &end
	if a := subscriptionAccess(account, now.AddDate(5, 0, 0)); a.PeriodStart.After(*a.PeriodEnd) || !a.PeriodStart.Equal(now) {
		t.Fatal("expired period dates drifted", a)
	}
	if a := subscriptionAccess(account, end); a.Status != "expired" || a.HasAccess {
		t.Fatal(a)
	}
	account.IsActive = false
	if a := subscriptionAccess(account, now); a.Status != "businessInactive" || a.HasAccess {
		t.Fatal(a)
	}
	if a := subscriptionAccess(BusinessAccount{Base: Base{IsActive: true}}, now); a.Status != "required" || a.HasAccess {
		t.Fatal(a)
	}
}
func TestPlanValidationAndCapacity(t *testing.T) {
	in := PlanInput{Name: "Starter", SKULimit: 10, WarehouseLimit: 1, PartnerLimit: 0, TrialDays: 14, BillingInterval: "monthly"}
	if err := validatePlan(in); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PlanInput){func(p *PlanInput) { p.SKULimit = 0 }, func(p *PlanInput) { p.WarehouseLimit = 0 }, func(p *PlanInput) { p.PartnerLimit = -1 }, func(p *PlanInput) { p.TrialDays = 366 }, func(p *PlanInput) { p.BillingInterval = "daily" }, func(p *PlanInput) { p.Name = "" }} {
		p := in
		change(&p)
		if validatePlan(p) == nil {
			t.Fatal(p)
		}
	}
	p := Plan{PlanInput: in}
	if !withinPlan(p, PlanUsage{SKUs: 10, Warehouses: 1}) || withinPlan(p, PlanUsage{SKUs: 11}) || withinPlan(p, PlanUsage{Partners: 1}) {
		t.Fatal("capacity check")
	}
}
func TestPlatformJWTIsNeverATenantJWT(t *testing.T) {
	s := &Store{secret: []byte(strings.Repeat("s", 32)), now: time.Now}
	for _, u := range []User{{AccountID: platformAccount, Role: Admin}, {AccountID: "ACC-000001", Role: SuperAdmin}, {AccountID: "", Role: SuperAdmin}} {
		u.UserID = "USR-000001"
		token, _ := s.token(u)
		if _, err := s.verify(token); err == nil {
			t.Fatal("cross-scope token accepted", u.Role, u.AccountID)
		}
	}
	token, _ := s.token(User{UserID: "USR-000001", AccountID: platformAccount, Role: SuperAdmin})
	if _, err := s.verify(token); err != nil {
		t.Fatal(err)
	}
}
