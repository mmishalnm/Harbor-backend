package api

import (
	"harbor/internal/store"
	"strings"
	"sync"
	"testing"
)

func pickupOrder(t *testing.T, c testClient, f fixture, quantity int64) store.Order {
	t.Helper()
	return must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "counter", FulfillmentType: store.CustomerPickupFulfillment, PickupLocationID: f.location.LocationID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: quantity}}}, 201)
}
func TestMongoCustomerPickupInvoiceStockMoneyAndStaffAccess(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 100}, 201)
	old, _ := f.deliver(t, f.customer.CustomerID, 1, 0)
	second := must[store.Variant](t, c, "POST", "/api/v1/variants", store.VariantInput{ProductID: f.variant.ProductID, Name: "2.5kg", Price: 65}, 201)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: second.VariantID, Quantity: 10}, 201)
	in := store.OrderInput{CustomerID: f.customer.CustomerID, Source: "counter", FulfillmentType: store.CustomerPickupFulfillment, PickupLocationID: f.location.LocationID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 20}, {VariantID: second.VariantID, Quantity: 5}}, Discount: &store.DiscountInput{Type: "fixed", Value: 100, Reason: "Loyalty"}}
	o := must[store.Order](t, c, "POST", "/api/v1/orders", in, 201)
	stocks := must[store.List[store.Stock]](t, c, "GET", "/api/v1/stocks?variantId="+f.variant.VariantID, nil, 200)
	if o.TripID != "" || stocks.Items[0].Quantity != 99 || stocks.Items[0].ReservedQuantity != 20 || stocks.Items[0].AvailableQuantity != 79 {
		t.Fatal(o, stocks)
	}
	staff := createUser(t, c, "counterstaff", store.WarehouseStaff)
	must[store.User](t, c, "PUT", "/api/v1/users/"+staff.UserID+"/locations", map[string]any{"locationIds": []string{f.location.LocationID}}, 200)
	actor := loginAs(t, c, "counterstaff")
	must[store.Order](t, actor, "GET", "/api/v1/orders/"+o.OrderID, nil, 200)
	rows := must[store.List[store.Order]](t, actor, "GET", "/api/v1/orders?fulfillmentType=customerPickup", nil, 200)
	if rows.Total != 1 {
		t.Fatal(rows)
	}
	collection := store.CustomerCollectionInput{Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 18}, {VariantID: second.VariantID, Quantity: 5}}, CollectedAmount: 3500, PaymentMethod: "cash", Reason: "Two bags not collected"}
	result := must[store.CustomerCollectionResult](t, actor, "POST", "/api/v1/orders/"+o.OrderID+"/collect", collection, 200)
	if result.Order.Status != "partiallyCollected" || result.Invoice.TripID != "" || result.Payment.TripID != "" || result.Invoice.Total != 2393.81 || result.Invoice.DiscountAmount != 91.19 || len(result.Order.ReservedItems) != 0 {
		t.Fatal(result)
	}
	if !strings.HasSuffix(result.Invoice.InvoiceNo, "/00002") {
		t.Fatal(result.Invoice.InvoiceNo)
	}
	for _, m := range result.Movements {
		if m.FromLocationID != f.location.LocationID || m.ToLocationID != "" || m.TripID != "" || m.Kind != "customerPickup" || m.ReferenceID != result.Invoice.InvoiceID {
			t.Fatal(m)
		}
	}
	ledger := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	if ledger.InvoiceTotal != 2513.81 || ledger.AdvanceCredit != 986.19 || ledger.Balance != -986.19 {
		t.Fatal(ledger)
	}
	must[store.Invoice](t, actor, "GET", "/api/v1/invoices/"+result.Invoice.InvoiceID, nil, 200)
	wantError(t, actor, "GET", "/api/v1/invoices/"+old.Invoice.InvoiceID, nil, 403, "FORBIDDEN")
	wantError(t, actor, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 403, "FORBIDDEN")
	wantError(t, actor, "POST", "/api/v1/orders/"+o.OrderID+"/collect", collection, 409, "ORDER_ALREADY_CLOSED")
	stocks = must[store.List[store.Stock]](t, c, "GET", "/api/v1/stocks?variantId="+f.variant.VariantID, nil, 200)
	if stocks.Items[0].Quantity != 81 || stocks.Items[0].ReservedQuantity != 0 {
		t.Fatal(stocks)
	}
	sales := must[store.SalesAnalytics](t, c, "GET", "/api/v1/analytics/sales", nil, 200)
	if sales.Summary.SalesAmount != 2513.81 {
		t.Fatal(sales)
	}
}
func TestMongoCustomerPickupGuardsAndAtomicFailure(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	o := pickupOrder(t, c, f, 3)
	path := "/api/v1/orders/" + o.OrderID + "/collect"
	input := store.CustomerCollectionInput{Items: o.Items}
	wantError(t, c, "POST", path, input, 409, "INSUFFICIENT_STOCK")
	if invoices := must[store.List[store.Invoice]](t, c, "GET", "/api/v1/invoices", nil, 200); invoices.Total != 0 {
		t.Fatal(invoices)
	}
	if open := must[store.Order](t, c, "GET", "/api/v1/orders/"+o.OrderID, nil, 200); open.Status != "open" || open.Collection != nil {
		t.Fatal(open)
	}
	otherLoc := must[store.Location](t, c, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: f.location.WarehouseID, Name: "Other freezer", Type: "freezer"}, 201)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: otherLoc.LocationID, VariantID: f.variant.VariantID, Quantity: 10}, 201)
	// Reservations are business-wide, but collection must debit the exact location.
	wantError(t, c, "POST", path, input, 409, "INSUFFICIENT_STOCK")
	staff := createUser(t, c, "restrictedcounter", store.WarehouseStaff)
	must[store.User](t, c, "PUT", "/api/v1/users/"+staff.UserID+"/locations", map[string]any{"locationIds": []string{otherLoc.LocationID}}, 200)
	actor := loginAs(t, c, "restrictedcounter")
	wantError(t, actor, "GET", "/api/v1/orders/"+o.OrderID, nil, 404, "NOT_FOUND")
	wantError(t, actor, "POST", path, input, 409, "LOCATION_NOT_ASSIGNED")
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Van"}, 201)
	wantError(t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{o.OrderID}}, 409, "PICKUP_ORDER_REQUIRES_COLLECTION")
	wantError(t, c, "POST", "/api/v1/orders/"+o.OrderID+"/assign", store.AssignOrderInput{DeliveryPartnerID: f.driver.UserID, VehicleID: van.VehicleID}, 409, "PICKUP_ORDER_REQUIRES_COLLECTION")
	foreign, _ := signupBusiness(t, c, "foreignpickup", "Other business")
	wantError(t, foreign, "GET", "/api/v1/orders/"+o.OrderID, nil, 404, "NOT_FOUND")
	wantError(t, foreign, "POST", path, input, 404, "NOT_FOUND")
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 3}, 201)
	result := must[store.CustomerCollectionResult](t, c, "POST", path, input, 200)
	if result.Invoice.Total != 360 || result.Payment != nil || !strings.HasSuffix(result.Invoice.InvoiceNo, "/00001") {
		t.Fatal(result)
	}
}
func TestMongoCustomerPickupConcurrentCollectionAndRiderAutoTrip(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.OrderSettings](t, c, "PUT", "/api/v1/account/order-settings", map[string]any{"autoAssignDeliveryPartnerOrders": true, "tripDurationDays": 1}, 200)
	rider := loginAs(t, c, "driver")
	o := pickupOrder(t, rider, f, 1) // No assigned vehicle required, even with auto-trip enabled.
	if o.TripID != "" {
		t.Fatal(o)
	}
	other := createUser(t, c, "otherpickupdriver", store.DeliveryPartner)
	_ = other
	wantError(t, loginAs(t, c, "otherpickupdriver"), "POST", "/api/v1/orders/"+o.OrderID+"/collect", store.CustomerCollectionInput{Items: o.Items}, 403, "FORBIDDEN")
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := rider.call("POST", "/api/v1/orders/"+o.OrderID+"/collect", store.CustomerCollectionInput{Items: o.Items, CollectedAmount: 200, PaymentMethod: "cash"})
			results <- code
		}()
	}
	wg.Wait()
	close(results)
	success, closed := 0, 0
	for code := range results {
		if code == 200 {
			success++
		} else if code == 409 {
			closed++
		} else {
			t.Fatal(code)
		}
	}
	if success != 1 || closed != 1 {
		t.Fatal(success, closed)
	}
	if invoices := must[store.List[store.Invoice]](t, c, "GET", "/api/v1/invoices", nil, 200); invoices.Total != 1 {
		t.Fatal(invoices)
	}
	if trips := must[store.List[store.Trip]](t, c, "GET", "/api/v1/trips", nil, 200); trips.Total != 0 {
		t.Fatal("collection created trip", trips)
	}
	invoice := must[store.Order](t, rider, "GET", "/api/v1/orders/"+o.OrderID, nil, 200)
	must[store.Invoice](t, rider, "GET", "/api/v1/invoices/"+invoice.Collection.InvoiceID, nil, 200)
}
