package api

import (
	"net/url"
	"testing"
	"time"

	"harbor/internal/store"
)

func TestMongoPartnerCollectionWithoutAnyTripAndCashSettlement(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	payment := must[store.Payment](t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 3500, Method: "cash"}, 201)
	if payment.TripID != "" || payment.ReceivedBy != f.driver.UserID {
		t.Fatal("independent collection ownership wrong", payment)
	}
	for _, resource := range []string{"orders", "trips", "stockMovements", "stocks"} {
		rows := must[store.List[map[string]any]](t, c, "GET", "/api/v1/"+resource, nil, 200)
		if rows.Total != 0 {
			t.Fatal("collection created unrelated operational records", resource, rows)
		}
	}
	must[store.Payment](t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 500, Method: "upi"}, 201)
	must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 750, Method: "cash"}, 201)
	ledger := cashLedger(t, c, f.driver.UserID)
	if ledger.Summary.CashHeld != 3500 || ledger.Summary.TotalCollected != 4000 || ledger.Summary.UpiCollected != 500 {
		t.Fatal("independent cash or office counter collections attributed wrongly", ledger)
	}
	settled := must[store.PartnerCashResult](t, c, "POST", "/api/v1/partners/"+f.driver.UserID+"/cash-settlements", store.PartnerCashInput{Amount: 500, Method: "cashHandover", Note: "Independent collection handed over", RequestID: "independent-handover-01"}, 201)
	if settled.Summary.CashHeld != 3000 || len(settled.Entry.Allocations) != 1 || settled.Entry.Allocations[0].PaymentID != payment.PaymentID || settled.Entry.Allocations[0].TripID != "" {
		t.Fatal("standalone receipt not reconcilable", settled)
	}
	due := 10000.0
	adjusted := must[store.PartnerCashResult](t, c, "POST", "/api/v1/partners/"+f.driver.UserID+"/cash-settlements", store.PartnerCashInput{Amount: 3000, Method: "salaryAdjustment", Note: "November salary offset", RequestID: "independent-salary-001", SalaryDue: &due}, 201)
	if adjusted.Summary.CashHeld != 0 || adjusted.Entry.SalaryPayable == nil || *adjusted.Entry.SalaryPayable != 7000 {
		t.Fatal("independent cash salary offset wrong", adjusted)
	}
	customer := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	if customer.PaymentTotal != 4750 || customer.AdvanceCredit != 4750 || customer.Balance != -4750 {
		t.Fatal("partner settlement altered customer balance", customer)
	}
	other := createUser(t, c, "independentother", store.DeliveryPartner)
	if cashLedger(t, c, other.UserID).Summary.CashHeld != 0 {
		t.Fatal("independent payment attributed to another partner")
	}
	wantError(t, rider, "GET", "/api/v1/partners/"+other.UserID+"/cash", nil, 403, "FORBIDDEN")
	foreign, _ := signupBusiness(t, c, "collectionforeign", "Foreign business")
	ff := setupFixture(t, foreign, "collectionforeign")
	if cashLedger(t, foreign, ff.driver.UserID).Summary.TotalCollected != 0 {
		t.Fatal("independent collections crossed businesses")
	}
	foreignOnly := must[store.Customer](t, foreign, "POST", "/api/v1/customers", store.CustomerInput{Name: "Foreign only store"}, 201)
	wantError(t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: foreignOnly.CustomerID, Amount: 1, Method: "cash"}, 404, "NOT_FOUND")
}
func TestMongoIndependentCollectionAfterDeliveryAllocatesOldestInvoice(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 2}, 201)
	first, trip1 := f.deliver(t, f.customer.CustomerID, 1, 0)
	second, trip2 := f.deliver(t, f.customer.CustomerID, 1, 0)
	for _, trip := range []store.Trip{trip1, trip2} {
		must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/complete", nil, 200)
	}
	rider := loginAs(t, c, "driver")
	wantError(t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, TripID: trip2.TripID, Amount: 1, Method: "cash"}, 409, "TRIP_NOT_STARTED")
	must[store.Payment](t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 150, Method: "cash"}, 201)
	allocations := must[store.List[store.Allocation]](t, c, "GET", "/api/v1/paymentAllocations", nil, 200)
	paid := map[string]float64{}
	for _, a := range allocations.Items {
		paid[a.InvoiceID] += a.Amount
	}
	if paid[first.Invoice.InvoiceID] != 120 || paid[second.Invoice.InvoiceID] != 30 {
		t.Fatal("independent collection lost oldest-first allocation", paid)
	}
	for _, trip := range []store.Trip{trip1, trip2} {
		summary := must[store.TripCashSummary](t, c, "GET", "/api/v1/trips/"+trip.TripID+"/cash-summary", nil, 200)
		if summary.CashCollected != 0 {
			t.Fatal("independent cash was added to a delivery trip", summary)
		}
	}
	if cashLedger(t, c, f.driver.UserID).Summary.CashHeld != 150 {
		t.Fatal("partner is not holding later collection")
	}
}
func TestMongoOrderFiltersCombineAcrossPagesAndPreserveAssignments(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	otherCustomer := must[store.Customer](t, c, "POST", "/api/v1/customers", store.CustomerInput{Name: "Other store"}, 201)
	one, two := reserveOrder(t, c, f, 1), reserveOrder(t, c, f, 1)
	three := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: otherCustomer.CustomerID, Source: "counter", Items: one.Items}, 201)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Filter van"}, 201)
	trip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{DeliveryPartnerID: f.driver.UserID, VehicleID: van.VehicleID, OrderIDs: []string{three.OrderID}}, 201)
	params := url.Values{"status": {"open"}, "source": {"phone"}, "customerId": {f.customer.CustomerID}, "page": {"1"}, "limit": {"1"}}
	date := time.Now().In(time.FixedZone("IST", 19800)).Format("2006-01-02")
	params.Set("dateFrom", date)
	params.Set("dateTo", date)
	rows := must[store.List[store.Order]](t, c, "GET", "/api/v1/orders?"+params.Encode(), nil, 200)
	if rows.Total != 2 || len(rows.Items) != 1 || rows.Items[0].OrderID != two.OrderID {
		t.Fatal("filters ignored or only applied on current page", rows)
	}
	params.Set("page", "2")
	rows = must[store.List[store.Order]](t, c, "GET", "/api/v1/orders?"+params.Encode(), nil, 200)
	if rows.Total != 2 || len(rows.Items) != 1 || rows.Items[0].OrderID != one.OrderID {
		t.Fatal("filtered pagination lost orders", rows)
	}
	path := "/api/v1/orders?status=assigned&deliveryPartnerId=" + f.driver.UserID + "&tripId=" + trip.TripID
	rows = must[store.List[store.Order]](t, c, "GET", path, nil, 200)
	if rows.Total != 1 || rows.Items[0].OrderID != three.OrderID {
		t.Fatal("partner/trip filter failed", rows)
	}
	other := createUser(t, c, "filterotherrider", store.DeliveryPartner)
	rows = must[store.List[store.Order]](t, loginAs(t, c, "filterotherrider"), "GET", path, nil, 200)
	if rows.Total != 0 {
		t.Fatal("filters bypassed rider scope", rows)
	}
	rows = must[store.List[store.Order]](t, c, "GET", path+"&customerId="+f.customer.CustomerID, nil, 200)
	if rows.Total != 0 {
		t.Fatal("filters did not combine with AND")
	}
	rows = must[store.List[store.Order]](t, c, "GET", "/api/v1/orders?status=assigned&deliveryPartnerId="+other.UserID+"&tripId="+trip.TripID, nil, 200)
	if rows.Total != 0 {
		t.Fatal("partner filter overwrote explicit trip filter")
	}
	for _, query := range []string{"status=unknown", "dateFrom=2026-02-30", "dateFrom=2026-11-20&dateTo=2026-11-19"} {
		wantError(t, c, "GET", "/api/v1/orders?"+query, nil, 400, "VALIDATION_ERROR")
	}
}

func TestMongoMixedTripAndIndependentCashSettlesWithoutDoubleCounting(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	_, trip := f.deliver(t, f.customer.CustomerID, 1, 1000)
	rider := loginAs(t, c, "driver")
	independent := must[store.Payment](t, rider, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 500, Method: "cash"}, 201)
	offset := must[store.PartnerCashResult](t, c, "POST", "/api/v1/partners/"+f.driver.UserID+"/cash-settlements", store.PartnerCashInput{Amount: 1200, Method: "salaryAdjustment", Note: "Mixed collection salary offset", RequestID: "mixed-source-salary-0001"}, 201)
	if offset.Summary.CashHeld != 300 || len(offset.Entry.Allocations) != 2 || offset.Entry.Allocations[0].TripID != trip.TripID || offset.Entry.Allocations[0].Amount != 1000 || offset.Entry.Allocations[1].PaymentID != independent.PaymentID || offset.Entry.Allocations[1].Amount != 200 {
		t.Fatal("cash sources not allocated oldest-first", offset)
	}
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/complete", nil, 200)
	zero := 0.0
	settled := must[store.Settlement](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/settle", store.SettlementInput{CashHandedOver: &zero, Note: "Trip cash covered by salary offset"}, 201)
	if settled.Difference != 0 || settled.SalaryAdjusted != 1000 || cashLedger(t, c, f.driver.UserID).Summary.CashHeld != 300 {
		t.Fatal("independent collection was mixed into trip settlement", settled)
	}
	returned := must[store.PartnerCashResult](t, c, "POST", "/api/v1/partners/"+f.driver.UserID+"/cash-settlements", store.PartnerCashInput{Amount: 300, Method: "cashHandover", Note: "Remaining independent cash returned", RequestID: "mixed-source-handover-001"}, 201)
	if returned.Summary.CashHeld != 0 || len(returned.Entry.Allocations) != 1 || returned.Entry.Allocations[0].PaymentID != independent.PaymentID {
		t.Fatal("independent balance was not cleared", returned)
	}
}
