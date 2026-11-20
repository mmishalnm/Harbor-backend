package api

import (
	"bytes"
	"fmt"
	"harbor/internal/store"
	"testing"
)

func TestMongoRiderStockUpdatesRespectAssignmentsAndAudit(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	other := must[store.Location](t, c, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: f.location.WarehouseID, Name: "Other freezer", Type: "freezer"}, 201)
	must[store.Movement](t, rider, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 10}, 201)
	moved := must[store.Movement](t, rider, "POST", "/api/v1/stock/adjust", store.AdjustmentInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, QuantityDelta: -2, Reason: " Damaged bags "}, 201)
	if moved.ActorID != f.driver.UserID || moved.Reason != "Damaged bags" || stockAt(t, c, f.location.LocationID, f.variant.VariantID) != 8 {
		t.Fatal(moved)
	}
	wantError(t, rider, "POST", "/api/v1/stock/adjust", store.AdjustmentInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, QuantityDelta: -9, Reason: "Count"}, 409, "INSUFFICIENT_STOCK")
	must[store.Movement](t, rider, "POST", "/api/v1/stock/transfer", store.TransferInput{FromLocationID: f.location.LocationID, ToLocationID: other.LocationID, VariantID: f.variant.VariantID, Quantity: 2}, 201)
	must[store.User](t, c, "PUT", "/api/v1/users/"+f.driver.UserID+"/locations", map[string]any{"locationIds": []string{f.location.LocationID}}, 200)
	rider = loginAs(t, c, "driver")
	wantError(t, rider, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: other.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 409, "LOCATION_NOT_ASSIGNED")
	wantError(t, rider, "POST", "/api/v1/stock/transfer", store.TransferInput{FromLocationID: f.location.LocationID, ToLocationID: other.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 409, "LOCATION_NOT_ASSIGNED")
	rows := must[store.List[store.Stock]](t, rider, "GET", "/api/v1/stocks?locationId="+other.LocationID, nil, 200)
	if rows.Total != 0 {
		t.Fatal(rows)
	}
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Van"}, 201)
	wantError(t, rider, "POST", "/api/v1/stock/adjust", store.AdjustmentInput{LocationID: van.VehicleID, VariantID: f.variant.VariantID, QuantityDelta: 1, Reason: "Count"}, 404, "NOT_FOUND")
	createUser(t, c, "warehouse", store.WarehouseStaff)
	staff := loginAs(t, c, "warehouse")
	wantError(t, staff, "POST", "/api/v1/stock/adjust", store.AdjustmentInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, QuantityDelta: 1, Reason: "Count"}, 403, "FORBIDDEN")
	movements := must[store.List[store.Movement]](t, c, "GET", "/api/v1/stockMovements?kind=transfer&locationId="+other.LocationID, nil, 200)
	if movements.Total != 1 || movements.Items[0].ToLocationID != other.LocationID {
		t.Fatal(movements)
	}
}
func TestMongoLedgerHistoryFiltersExportAndIsolation(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	for i, method := range []string{"cash", "upi", "cash"} {
		must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: float64(i+1) * 100, Method: method}, 201)
	}
	path := "/api/v1/customers/" + f.customer.CustomerID + "/ledger"
	all := must[store.Ledger](t, c, "GET", path, nil, 200)
	if len(all.Entries) != 3 || all.Entries[0].Amount != -300 || all.Balance != -600 {
		t.Fatal(all)
	}
	filtered := must[store.Ledger](t, c, "GET", path+"?type=payment&method=cash&limit=1&page=2", nil, 200)
	if filtered.Total != 2 || len(filtered.Entries) != 1 || filtered.Entries[0].Amount != -100 || filtered.Balance != -600 || filtered.AdvanceCredit != 600 {
		t.Fatal(filtered)
	}
	for _, filter := range []string{"method=crypto", "dateFrom=invalid", "type=all"} {
		wantError(t, c, "GET", path+"?"+filter, nil, 400, "VALIDATION_ERROR")
	}
	for i := 0; i < 20; i++ {
		must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, Amount: 1, Method: "cash"}, 201)
	}
	export := must[store.LedgerDocument](t, c, "GET", path+"/export?method=cash", nil, 200)
	if export.FileName != "Sunrise-Stores-ledger.pdf" || export.MIMEType != "application/pdf" || !bytes.HasPrefix(export.Data, []byte("%PDF-")) {
		t.Fatal("bad export")
	}
	if bytes.Count(export.Data, []byte("/Type /Page")) < 3 {
		t.Fatal("export truncated matching transactions to one page")
	}
	rider := loginAs(t, c, "driver")
	wantError(t, rider, "GET", path+"/export", nil, 403, "FORBIDDEN")
	trip, _ := f.trip(t, f.customer.CustomerID, 1)
	tripPath := fmt.Sprintf("/api/v1/trips/%s/customers/%s/ledger/export", trip.TripID, f.customer.CustomerID)
	must[store.LedgerDocument](t, rider, "GET", tripPath, nil, 200)
	createUser(t, c, "other_rider", store.DeliveryPartner)
	other := loginAs(t, c, "other_rider")
	wantError(t, other, "GET", tripPath, nil, 403, "FORBIDDEN")
	foreign, _ := signupBusiness(t, c, "exportforeign", "Another business")
	wantError(t, foreign, "GET", path+"/export", nil, 404, "NOT_FOUND")
	payments := must[store.List[store.Payment]](t, c, "GET", "/api/v1/payments?method=upi", nil, 200)
	if payments.Total != 1 || payments.Items[0].Amount != 200 {
		t.Fatal(payments)
	}
}
