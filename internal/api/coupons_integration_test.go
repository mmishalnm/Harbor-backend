package api

import (
	"strings"
	"sync"
	"testing"

	"harbor/internal/store"
)

func TestMongoCouponsSnapshotPartialDeliveryNetLedgerAndSales(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	coupon := must[store.Coupon](t, c, "POST", "/api/v1/coupons", store.CouponInput{Name: "Loyalty", Code: " save100 ", Type: "fixed", Value: 100}, 201)
	if !strings.HasPrefix(coupon.CouponID, "CPN-") || coupon.Code != "SAVE100" {
		t.Fatal(coupon)
	}
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 30}, 201)
	old, _ := f.deliver(t, f.customer.CustomerID, 1, 0)
	in := store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 10}}, CouponID: coupon.CouponID}
	o := must[store.Order](t, c, "POST", "/api/v1/orders", in, 201)
	if o.Discount == nil || o.Discount.Value != 100 || o.Discount.Code != "SAVE100" {
		t.Fatal(o)
	}
	must[store.Coupon](t, c, "PUT", "/api/v1/coupons/"+coupon.CouponID, store.CouponInput{Name: "Changed", Code: "SAVE100", Type: "fixed", Value: 500}, 200)
	must[map[string]bool](t, c, "POST", "/api/v1/coupons/"+coupon.CouponID+"/archive", nil, 200)
	wantError(t, c, "POST", "/api/v1/orders", in, 404, "NOT_FOUND")
	options := must[store.List[store.Coupon]](t, c, "GET", "/api/v1/coupons/options", nil, 200)
	if options.Total != 0 {
		t.Fatal(options)
	}
	must[store.Variant](t, c, "PATCH", "/api/v1/variants/"+f.variant.VariantID+"/price", map[string]float64{"price": 150}, 200)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Coupon van"}, 201)
	trip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{o.OrderID}}, 201)
	path := "/api/v1/trips/" + trip.TripID
	must[store.PickupResult](t, c, "POST", path+"/pickup", store.PickupInput{OrderID: o.OrderID, LocationID: f.location.LocationID, Items: o.Items}, 200)
	must[store.Trip](t, c, "POST", path+"/start", nil, 200)
	closeInput := store.CloseStopInput{Delivered: []store.Item{{VariantID: f.variant.VariantID, Quantity: 4}}, Reason: "Remaining goods declined", CollectedAmount: 700, PaymentMethod: "cash"}
	result := must[store.CloseStopResult](t, c, "POST", path+"/stops/"+o.OrderID+"/close", closeInput, 200)
	inv := result.Invoice
	if inv == nil || inv.Subtotal != 600 || inv.DiscountAmount != 40 || inv.Total != 560 || inv.Lines[0].UnitPrice != 150 || inv.Lines[0].Amount != 560 || inv.Discount.Value != 100 {
		t.Fatal(inv)
	}
	wantError(t, c, "POST", path+"/stops/"+o.OrderID+"/close", closeInput, 409, "STOP_ALREADY_CLOSED")
	ledger := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	if ledger.InvoiceTotal != 680 || ledger.PaymentTotal != 700 || ledger.AdvanceCredit != 20 || ledger.Balance != -20 {
		t.Fatal(ledger)
	}
	report := must[store.SalesAnalytics](t, c, "GET", "/api/v1/analytics/sales?variantId="+f.variant.VariantID, nil, 200)
	if report.Summary.SalesAmount != 680 || report.Products.Items[0].AverageUnitPrice != 136 {
		t.Fatal(report)
	}
	rows := must[store.List[store.Invoice]](t, c, "GET", "/api/v1/invoices", nil, 200)
	if rows.Total != 2 {
		t.Fatal(rows)
	}
	allocations := must[store.List[store.Allocation]](t, c, "GET", "/api/v1/paymentAllocations", nil, 200)
	paid := map[string]float64{}
	for _, v := range allocations.Items {
		paid[v.InvoiceID] += v.Amount
	}
	if paid[old.Invoice.InvoiceID] != 120 || paid[inv.InvoiceID] != 560 {
		t.Fatal(allocations)
	}
}

func TestMongoCouponPermissionsValidationIsolationAndFreeInvoice(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	createUser(t, c, "couponstaff", store.WarehouseStaff)
	staff := loginAs(t, c, "couponstaff")
	input := store.CouponInput{Name: "Welcome", Code: "WELCOME10", Type: "percentage", Value: 10}
	coupon := must[store.Coupon](t, c, "POST", "/api/v1/coupons", input, 201)
	wantError(t, c, "POST", "/api/v1/coupons", input, 409, "COUPON_CODE_EXISTS")
	wantError(t, c, "GET", "/api/v1/coupons?limit=201", nil, 400, "VALIDATION_ERROR")
	wantError(t, c, "GET", "/api/v1/coupons?isActive=invalid", nil, 400, "VALIDATION_ERROR")
	in := store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}, Discount: &store.DiscountInput{Type: "fixed", Value: 10, Reason: "Loyal customer"}}
	for _, actor := range []testClient{rider, staff} {
		wantError(t, actor, "POST", "/api/v1/coupons", input, 403, "FORBIDDEN")
		wantError(t, actor, "PUT", "/api/v1/coupons/"+coupon.CouponID, input, 403, "FORBIDDEN")
		wantError(t, actor, "POST", "/api/v1/coupons/"+coupon.CouponID+"/archive", nil, 403, "FORBIDDEN")
		wantError(t, actor, "GET", "/api/v1/coupons", nil, 403, "FORBIDDEN")
		options := must[store.List[store.Coupon]](t, actor, "GET", "/api/v1/coupons/options", nil, 200)
		if options.Total != 1 {
			t.Fatal(options)
		}
		wantError(t, actor, "POST", "/api/v1/orders", in, 403, "FORBIDDEN")
		approved := in
		approved.Discount = nil
		approved.CouponID = coupon.CouponID
		must[store.Order](t, actor, "POST", "/api/v1/orders", approved, 201)
	}
	for _, d := range []*store.DiscountInput{{Type: "fixed", Value: 121, Reason: "Too much"}, {Type: "percentage", Value: 101, Reason: "Too much"}, {Type: "fixed", Value: 10}, {Type: "fixed", Value: -1, Reason: "Negative"}} {
		in.Discount = d
		wantError(t, c, "POST", "/api/v1/orders", in, 400, "VALIDATION_ERROR")
	}
	in.Discount = &store.DiscountInput{Type: "fixed", Value: 10, Reason: "Loyalty"}
	in.CouponID = coupon.CouponID
	wantError(t, c, "POST", "/api/v1/orders", in, 400, "VALIDATION_ERROR")
	foreign, _ := signupBusiness(t, c, "couponforeign", "Other business")
	private := must[store.List[store.Coupon]](t, foreign, "GET", "/api/v1/coupons/options?accountId="+f.driver.AccountID, nil, 200)
	if private.Total != 0 {
		t.Fatal(private)
	}
	must[store.Coupon](t, foreign, "POST", "/api/v1/coupons", input, 201) // same code is legal in another business
	foreignCoupon := must[store.Coupon](t, foreign, "POST", "/api/v1/coupons", store.CouponInput{Name: "Private", Code: "PRIVATE", Type: "fixed", Value: 10}, 201)
	in.CouponID = foreignCoupon.CouponID
	in.Discount = nil
	wantError(t, c, "POST", "/api/v1/orders", in, 404, "NOT_FOUND")
	// A zero-net invoice is valid and consumes no advance; numbering stays gapless
	// despite all of the rejected creations above.
	in.CouponID = ""
	in.Discount = &store.DiscountInput{Type: "percentage", Value: 100, Reason: "Complimentary sample"}
	o := must[store.Order](t, c, "POST", "/api/v1/orders", in, 201)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 4}, 201)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Free sample van"}, 201)
	trip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{o.OrderID}}, 201)
	path := "/api/v1/trips/" + trip.TripID
	must[store.PickupResult](t, c, "POST", path+"/pickup", store.PickupInput{OrderID: o.OrderID, LocationID: f.location.LocationID, Items: o.Items}, 200)
	must[store.Trip](t, c, "POST", path+"/start", nil, 200)
	result := must[store.CloseStopResult](t, c, "POST", path+"/stops/"+o.OrderID+"/close", store.CloseStopInput{Delivered: o.Items}, 200)
	if result.Invoice.Total != 0 || result.Invoice.DiscountAmount != 120 || !strings.HasSuffix(result.Invoice.InvoiceNo, "/00001") {
		t.Fatal(result.Invoice)
	}
}

func TestMongoCouponConcurrentCodeUniqueness(t *testing.T) {
	c := testBackend(t)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := c.call("POST", "/api/v1/coupons", store.CouponInput{Name: "Offer", Code: "ONCE", Type: "fixed", Value: 10})
			results <- code
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for code := range results {
		switch code {
		case 201:
			success++
		case 409:
			conflict++
		default:
			t.Fatal(code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
