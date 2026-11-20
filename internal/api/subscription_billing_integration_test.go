package api

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"harbor/internal/store"
	"sync"
	"testing"
	"time"
)

func paidPlan(t *testing.T, c testClient, trial int) store.Plan {
	fee := 500.0
	return must[store.Plan](t, loginAs(t, c, "platformowner"), "POST", "/api/v1/platform/plans", store.PlanInput{Name: "Paid growth", Price: &fee, SKULimit: 100, WarehouseLimit: 5, PartnerLimit: 10, TrialDays: trial, BillingInterval: "monthly"}, 201)
}
func billingFixture(t *testing.T, c testClient, tenant testClient, trial int) (testClient, store.Plan, string) {
	owner := loginAs(t, c, "platformowner")
	plan := paidPlan(t, c, trial)
	must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": plan.PlanID}, 200)
	me := must[store.SessionProfile](t, tenant, "GET", "/api/v1/auth/me", nil, 200)
	return owner, plan, "/api/v1/platform/businesses/" + me.Account.AccountID + "/subscription"
}
func receipt(b store.SubscriptionBill, request string, amount float64) store.SubscriptionPaymentInput {
	return store.SubscriptionPaymentInput{BillID: b.BillID, RequestID: request, Amount: amount, Method: "cash", Note: "Collected manually by Harbor"}
}
func TestMongoManualSubscriptionPaymentsAndRenewal(t *testing.T) {
	c := testBackend(t)
	tenant := rawBusiness(t, c, "manualbilling")
	owner, plan, path := billingFixture(t, c, tenant, 0)
	if plan.Price != 500 {
		t.Fatal(plan)
	}
	// Omitted price on old clients' edits preserves the existing charge.
	changed := plan.PlanInput
	changed.Price = nil
	changed.Name = "Updated title"
	if p := must[store.Plan](t, owner, "PUT", "/api/v1/platform/plans/"+plan.PlanID, changed, 200); p.Price != 500 {
		t.Fatal(p)
	}
	changed.Price = new(float64)
	*changed.Price = 750
	must[store.Plan](t, owner, "PUT", "/api/v1/platform/plans/"+plan.PlanID, changed, 200)
	a := must[store.SubscriptionAccess](t, tenant, "GET", "/api/v1/subscription", nil, 200)
	if a.Plan.Price != 500 || a.Status != "paymentRequired" || a.HasAccess {
		t.Fatal(a)
	}
	wantError(t, tenant, "GET", "/api/v1/products", nil, 402, "SUBSCRIPTION_PAYMENT_REQUIRED")
	bill := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200)
	if again := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200); again.BillID != bill.BillID {
		t.Fatal("bill prepare duplicated")
	}
	wantError(t, tenant, "POST", path+"/payments", receipt(bill, "self-pay-request-123", 500), 403, "FORBIDDEN")
	wantError(t, owner, "POST", path+"/payments", receipt(bill, "overpay-request-123", 501), 400, "VALIDATION_ERROR")
	r := must[store.SubscriptionPaymentResult](t, owner, "POST", path+"/payments", receipt(bill, "partial-request-123", 200), 201)
	if r.Access.HasAccess || r.Bill.PaidAmount != 200 || r.Bill.Status != "pending" {
		t.Fatal(r)
	}
	wantError(t, tenant, "POST", "/api/v1/subscription/coupon", map[string]string{"code": "ANYCODE"}, 409, "BILL_LOCKED")
	wantError(t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": plan.PlanID}, 409, "BILL_HAS_PARTIAL_PAYMENT")
	input := receipt(bill, "full-pay-request-123", 300)
	r = must[store.SubscriptionPaymentResult](t, owner, "POST", path+"/payments", input, 201)
	if !r.Access.HasAccess || r.Bill.Status != "paid" || r.Bill.PaidAmount != 500 || r.Access.PaidUntil == nil {
		t.Fatal(r)
	}
	replay := must[store.SubscriptionPaymentResult](t, owner, "POST", path+"/payments", input, 201)
	if replay.Payment.PaymentID != r.Payment.PaymentID || !replay.Access.PaidUntil.Equal(*r.Access.PaidUntil) {
		t.Fatal("duplicate extended coverage")
	}
	input.Amount = 299
	wantError(t, owner, "POST", path+"/payments", input, 409, "IDEMPOTENCY_CONFLICT")
	must[store.List[store.Product]](t, tenant, "GET", "/api/v1/products", nil, 200)
	rows := must[store.List[store.SubscriptionPayment]](t, tenant, "GET", "/api/v1/subscription/payments?limit=1", nil, 200)
	if rows.Total != 2 || len(rows.Items) != 1 || rows.Items[0].Amount != 300 {
		t.Fatal(rows)
	}
	other := rawBusiness(t, c, "otherbilling")
	if rows := must[store.List[store.SubscriptionPayment]](t, other, "GET", "/api/v1/subscription/payments", nil, 200); rows.Total != 0 {
		t.Fatal("cross tenant receipts", rows)
	}
	// Platform access returns no tenant usage or sales and payments never create tenant finance rows.
	status := must[store.SubscriptionAccess](t, owner, "GET", path, nil, 200)
	if status.Usage != nil {
		t.Fatal(status)
	}
	for _, col := range []string{"invoices", "payments"} {
		n, e := c.db.Mongo.Collection(col).CountDocuments(context.Background(), bson.M{})
		if e != nil || n != 0 {
			t.Fatal(col, n, e)
		}
	}
	// Simulated expiry fixture uses only this disposable database.
	me := must[store.SessionProfile](t, tenant, "GET", "/api/v1/auth/me", nil, 200)
	past := time.Now().UTC().Add(-time.Hour)
	_, err := c.db.Mongo.Collection("accounts").UpdateOne(context.Background(), bson.M{"accountId": me.Account.AccountID}, bson.M{"$set": bson.M{"subscription.paidUntil": past}})
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, tenant, "GET", "/api/v1/products", nil, 402, "SUBSCRIPTION_PAYMENT_REQUIRED")
	next := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200)
	if next.Cycle != 2 || next.Total != 500 {
		t.Fatal(next)
	}
	r = must[store.SubscriptionPaymentResult](t, owner, "POST", path+"/payments", receipt(next, "renewal-request-123", 500), 201)
	if !r.Access.HasAccess || r.Bill.PeriodStart.Before(past) || r.Bill.PeriodStart.After(time.Now().UTC()) {
		t.Fatal(r)
	}
}
func TestMongoSubscriptionCouponsTrialAndWaiver(t *testing.T) {
	c := testBackend(t)
	tenant := rawBusiness(t, c, "couponbilling")
	owner, _, path := billingFixture(t, c, tenant, 14)
	coupon := must[store.Coupon](t, owner, "POST", "/api/v1/platform/subscription-coupons", store.CouponInput{Name: "Launch", Code: "LAUNCH10", Type: "percentage", Value: 10}, 201)
	a := must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription/coupon", map[string]string{"code": "launch10"}, 200)
	if a.Status != "trialing" || !a.HasAccess || a.Billing.Total != 450 {
		t.Fatal(a)
	}
	must[store.Coupon](t, owner, "PUT", "/api/v1/platform/subscription-coupons/"+coupon.CouponID, store.CouponInput{Name: "New offer", Code: "LAUNCH10", Type: "fixed", Value: 100}, 200)
	must[map[string]bool](t, owner, "POST", "/api/v1/platform/subscription-coupons/"+coupon.CouponID+"/archive", nil, 200)
	bill := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200)
	if bill.Total != 450 || bill.Discount.Type != "percentage" {
		t.Fatal("coupon snapshot changed", bill)
	}
	r := must[store.SubscriptionPaymentResult](t, owner, "POST", path+"/payments", receipt(bill, "trial-payment-1234", 450), 201)
	if !r.Access.HasAccess || !r.Bill.PeriodStart.Equal(*a.TrialEndsAt) {
		t.Fatal("early payment lost trial", r)
	}
	next := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200)
	if next.Total != 500 || next.Discount != nil {
		t.Fatal("coupon recurred", next)
	}
	// A zero net bill requires explicit platform approval and cannot be self-activated.
	tenant2 := rawBusiness(t, c, "waiverbilling")
	_, _, path2 := billingFixture(t, c, tenant2, 0)
	must[store.Coupon](t, owner, "POST", "/api/v1/platform/subscription-coupons", store.CouponInput{Name: "Complimentary", Code: "FREE100", Type: "percentage", Value: 100}, 201)
	a = must[store.SubscriptionAccess](t, tenant2, "POST", "/api/v1/subscription/coupon", map[string]string{"code": "FREE100"}, 200)
	if a.HasAccess || a.Billing.Total != 0 {
		t.Fatal(a)
	}
	b := must[store.SubscriptionBill](t, owner, "POST", path2+"/bill", nil, 200)
	in := receipt(b, "waiver-request-1234", 0)
	wantError(t, owner, "POST", path2+"/payments", in, 400, "VALIDATION_ERROR")
	in.Method = "waiver"
	r = must[store.SubscriptionPaymentResult](t, owner, "POST", path2+"/payments", in, 201)
	if !r.Access.HasAccess || r.Bill.Status != "paid" {
		t.Fatal(r)
	}
	wantError(t, tenant2, "POST", "/api/v1/platform/subscription-coupons", store.CouponInput{}, 403, "FORBIDDEN")
}
func TestMongoConcurrentBillingAndInactiveBusiness(t *testing.T) {
	c := testBackend(t)
	tenant := rawBusiness(t, c, "concurrentbilling")
	owner, _, path := billingFixture(t, c, tenant, 0)
	bill := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, id := range []string{"concurrent-pay-one", "concurrent-pay-two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			code, _ := owner.call("POST", path+"/payments", receipt(bill, id, 500))
			results <- code
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for code := range results {
		if code == 201 {
			success++
		} else if code == 409 {
			conflicts++
		} else {
			t.Fatal(code)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal(success, conflicts)
	}
	me := must[store.SessionProfile](t, tenant, "GET", "/api/v1/auth/me", nil, 200)
	active := false
	must[store.BusinessRegistration](t, owner, "PUT", "/api/v1/platform/businesses/"+me.Account.AccountID+"/status", store.BusinessStatusInput{IsActive: &active, Reason: "Review"}, 200)
	next := must[store.SubscriptionBill](t, owner, "POST", path+"/bill", nil, 200)
	r := must[store.SubscriptionPaymentResult](t, owner, "POST", path+"/payments", receipt(next, "inactive-payment-123", 500), 201)
	if r.Access.HasAccess || r.Access.Status != "businessInactive" {
		t.Fatal("receipt reactivated business", r)
	}
	wantError(t, tenant, "GET", "/api/v1/products", nil, 403, "BUSINESS_INACTIVE")
}

func TestMongoTenantExplicitlyAcceptsUpdatedPlanCharge(t *testing.T) {
	c := testBackend(t)
	owner := loginAs(t, c, "platformowner")
	before := must[store.SubscriptionAccess](t, c, "GET", "/api/v1/subscription", nil, 200)
	if !before.HasAccess || before.Plan.Price != 0 {
		t.Fatal(before)
	}
	input := before.Plan.PlanInput
	fee := 999.0
	input.Price = &fee
	must[store.Plan](t, owner, "PUT", "/api/v1/platform/plans/"+before.Plan.PlanID, input, 200)
	unchanged := must[store.SubscriptionAccess](t, c, "GET", "/api/v1/subscription", nil, 200)
	if unchanged.Plan.Price != 0 || !unchanged.HasAccess {
		t.Fatal("catalog edit silently repriced", unchanged)
	}
	after := must[store.SubscriptionAccess](t, c, "POST", "/api/v1/subscription", map[string]string{"planId": before.Plan.PlanID}, 200)
	if after.Plan.Price != 999 || after.HasAccess || after.Status != "paymentRequired" || after.TrialEndsAt != nil {
		t.Fatal(after)
	}
}
