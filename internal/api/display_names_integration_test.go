package api

import (
	"harbor/internal/store"
	"strings"
	"testing"
)

func TestMongoDisplayNamesPreserveAccountAndAssignmentAccess(t *testing.T) {
	root := testBackend(t)
	arun, _ := signupBusiness(t, root, "labelsarun", "Arun Frozen Foods")
	varun, _ := signupBusiness(t, root, "labelsvarun", "Varun Stationery")
	f := setupFixture(t, arun, "labels")
	otherCustomer := must[store.Customer](t, varun, "POST", "/api/v1/customers", store.CustomerInput{Name: "Varun private store"}, 201)
	if otherCustomer.CustomerID != f.customer.CustomerID {
		t.Fatal("test must exercise overlapping tenant-local IDs")
	}
	hiddenCustomer := must[store.Customer](t, arun, "POST", "/api/v1/customers", store.CustomerInput{Name: "Unassigned store"}, 201)
	staff := createUser(t, arun, "labelstaff", store.WarehouseStaff)
	hiddenStaff := createUser(t, arun, "hiddenlabelstaff", store.WarehouseStaff)
	trip, order := f.trip(t, f.customer.CustomerID, 1)
	sc := loginAs(t, arun, "labelstaff")
	rider := loginAs(t, arun, "labelsdriver")
	resolve := func(c testClient, ids ...string) map[string]string {
		return must[store.DisplayNamesResult](t, c, "POST", "/api/v1/display-names", store.DisplayNamesInput{IDs: ids}, 200).Names
	}
	before := resolve(sc, f.customer.CustomerID, f.location.LocationID, f.driver.UserID)
	if len(before) != 0 {
		t.Fatalf("unassigned staff saw labels: %v", before)
	}
	must[store.User](t, arun, "PUT", "/api/v1/users/"+staff.UserID+"/locations", map[string]any{"locationIds": []string{f.location.LocationID}}, 200)
	must[store.Trip](t, arun, "PUT", "/api/v1/trips/"+trip.TripID+"/staff", map[string]any{"warehouseStaffIds": []string{staff.UserID}}, 200)
	// Refresh the staff actor from their current assignments using the same JWT.
	ids := []string{f.variant.VariantID, f.location.LocationID, f.customer.CustomerID, f.driver.UserID, hiddenCustomer.CustomerID, hiddenStaff.UserID, order.OrderID, trip.TripID}
	adminNames := resolve(arun, ids...)
	if adminNames[f.variant.VariantID] != "ICE · 5kg" || adminNames[f.location.LocationID] != "Main Freezer · Kahakootam" || adminNames[f.customer.CustomerID] != "Sunrise Stores" {
		t.Fatal(adminNames)
	}
	for _, c := range []testClient{sc, rider} {
		names := resolve(c, ids...)
		if names[f.customer.CustomerID] != "Sunrise Stores" || names[f.driver.UserID] != "labelsdriver" || names[hiddenCustomer.CustomerID] != "" || names[hiddenStaff.UserID] != "" {
			t.Fatal("assignment leak or missing visible name", names)
		}
		if strings.Contains(strings.Join(mapValues(names), " "), "Varun") {
			t.Fatal("cross-business label leak")
		}
	}
	if resolve(varun, otherCustomer.CustomerID)[otherCustomer.CustomerID] != "Varun private store" {
		t.Fatal("wrong account label")
	}
	must[store.Movement](t, arun, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	must[store.PickupResult](t, arun, "POST", "/api/v1/trips/"+trip.TripID+"/pickup", store.PickupInput{OrderID: order.OrderID, LocationID: f.location.LocationID, Items: order.Items}, 200)
	must[store.Trip](t, arun, "POST", "/api/v1/trips/"+trip.TripID+"/start", nil, 200)
	delivered := must[store.CloseStopResult](t, rider, "POST", "/api/v1/trips/"+trip.TripID+"/stops/"+order.OrderID+"/close", store.CloseStopInput{Delivered: order.Items, PaymentMethod: "cash"}, 200)
	if resolve(rider, delivered.Invoice.InvoiceID)[delivered.Invoice.InvoiceID] != delivered.Invoice.InvoiceNo {
		t.Fatal("rider invoice label missing")
	}
	if len(resolve(sc, delivered.Invoice.InvoiceID)) != 0 {
		t.Fatal("warehouse staff saw invoice details")
	}
	wantError(t, arun, "POST", "/api/v1/display-names", store.DisplayNamesInput{IDs: []string{"not-a-business-id"}}, 400, "VALIDATION_ERROR")
}
func mapValues(m map[string]string) []string {
	out := []string{}
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
