package api

import (
	"encoding/json"
	"sync"
	"testing"

	"harbor/internal/store"
)

func TestMongoRiderCustomerOptionsBeforeFirstOrder(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	alpha := must[store.Customer](t, c, "POST", "/api/v1/customers", store.CustomerInput{Name: "Alpha Stores", Phone: "private-phone", Address: "private-address"}, 201)
	archived := must[store.Customer](t, c, "POST", "/api/v1/customers", store.CustomerInput{Name: "Archived Store"}, 201)
	must[map[string]any](t, c, "POST", "/api/v1/customers/"+archived.CustomerID+"/archive", nil, 200)
	other, _ := signupBusiness(t, c, "optionsother", "Other business")
	must[store.Customer](t, other, "POST", "/api/v1/customers", store.CustomerInput{Name: "Other private store"}, 201)
	rows := must[store.List[store.CustomerOption]](t, rider, "GET", "/api/v1/customers/options?page=1&limit=1", nil, 200)
	if rows.Total != 2 || len(rows.Items) != 1 || rows.Items[0].CustomerID != alpha.CustomerID || rows.Items[0].Name != alpha.Name {
		t.Fatal("wrong active customer selection", rows)
	}
	second := must[store.List[store.CustomerOption]](t, rider, "GET", "/api/v1/customers/options?page=2&limit=1", nil, 200)
	if len(second.Items) != 1 || second.Items[0].CustomerID != f.customer.CustomerID {
		t.Fatal("pagination lost a customer", second)
	}
	minimal := must[struct{ Items []map[string]any }](t, rider, "GET", "/api/v1/customers/options", nil, 200)
	for _, row := range minimal.Items {
		if len(row) != 2 || row["customerId"] == nil || row["name"] == nil {
			t.Fatal("customer option exposes extra data", row)
		}
	}
	labels := must[store.DisplayNamesResult](t, rider, "POST", "/api/v1/display-names", store.DisplayNamesInput{IDs: []string{alpha.CustomerID}}, 200)
	if labels.Names[alpha.CustomerID] != alpha.Name {
		t.Fatal("new customer label unavailable before first order", labels)
	}
	wantError(t, rider, "GET", "/api/v1/customers", nil, 403, "FORBIDDEN")
	wantError(t, rider, "GET", "/api/v1/customers/"+alpha.CustomerID+"/ledger", nil, 403, "FORBIDDEN")
	wantError(t, rider, "POST", "/api/v1/customers", store.CustomerInput{Name: "Not allowed"}, 403, "FORBIDDEN")
	wantError(t, rider, "GET", "/api/v1/customers/options?limit=201", nil, 400, "VALIDATION_ERROR")
	createUser(t, c, "optionstaff", store.WarehouseStaff)
	staff := loginAs(t, c, "optionstaff")
	wantError(t, staff, "GET", "/api/v1/customers/options", nil, 403, "FORBIDDEN")
	order := must[store.Order](t, rider, "POST", "/api/v1/orders", store.OrderInput{CustomerID: alpha.CustomerID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}, 201)
	if order.CustomerID != alpha.CustomerID || order.TripID != "" {
		t.Fatal("could not create first order with selected customer", order)
	}
}

func TestMongoOfficeOrderAssignmentCreatesReusesAndClaimsOnce(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	createUser(t, c, "assignmanager", store.Manager)
	manager := loginAs(t, c, "assignmanager")
	setAutoTrips(t, c, false, 2)
	newOrder := func() store.Order {
		return must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}, 201)
	}
	order := newOrder()
	path := "/api/v1/orders/" + order.OrderID + "/assign"
	in := store.AssignOrderInput{DeliveryPartnerID: f.driver.UserID}
	wantError(t, rider, "POST", path, in, 403, "FORBIDDEN")
	wantError(t, c, "POST", path, store.AssignOrderInput{}, 400, "VALIDATION_ERROR")
	wantError(t, c, "POST", path, in, 409, "DELIVERY_VEHICLE_NOT_ASSIGNED")
	trips := must[store.List[store.Trip]](t, c, "GET", "/api/v1/trips", nil, 200)
	if trips.Total != 0 {
		t.Fatal("failed assignment created a trip")
	}
	bad := in
	bad.DeliveryPartnerID = "USR-000001"
	wantError(t, c, "POST", path, bad, 400, "VALIDATION_ERROR")
	vehicle := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Assigned van"}, 201)
	in.VehicleID = vehicle.VehicleID
	assigned := must[store.Order](t, c, "POST", path, in, 200)
	trip := must[store.Trip](t, rider, "GET", "/api/v1/trips/"+assigned.TripID, nil, 200)
	if assigned.Status != "assigned" || trip.DeliveryPartnerID != f.driver.UserID || trip.VehicleID != vehicle.VehicleID || trip.DurationDays != 2 || len(trip.Stops) != 1 {
		t.Fatal("wrong assignment", assigned, trip)
	}
	wantError(t, c, "POST", path, in, 409, "ORDER_ALREADY_ASSIGNED")
	second := newOrder()
	// Omit vehicle: reuse the current trip even without a default vehicle.
	attached := must[store.Order](t, manager, "POST", "/api/v1/orders/"+second.OrderID+"/assign", store.AssignOrderInput{DeliveryPartnerID: f.driver.UserID}, 200)
	if attached.TripID != trip.TripID {
		t.Fatal("existing trip not reused")
	}
	third := newOrder()
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, actor := range []testClient{c, manager} {
		wg.Add(1)
		go func(actor testClient) {
			defer wg.Done()
			code, _ := actor.call("POST", "/api/v1/orders/"+third.OrderID+"/assign", store.AssignOrderInput{DeliveryPartnerID: f.driver.UserID})
			codes <- code
		}(actor)
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 200 {
			success++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatal("unexpected claim result", code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("order claimed more than once", success, conflict)
	}
	trip = must[store.Trip](t, c, "GET", "/api/v1/trips/"+trip.TripID, nil, 200)
	if len(trip.Stops) != 3 {
		t.Fatal("duplicate or missing stops", trip.Stops)
	}
	movements := must[store.List[store.Movement]](t, c, "GET", "/api/v1/stockMovements", nil, 200)
	if movements.Total != 0 || stockAt(t, c, vehicle.VehicleID, f.variant.VariantID) != 0 {
		t.Fatal("assignment moved stock")
	}
	own := must[store.List[store.Order]](t, rider, "GET", "/api/v1/orders", nil, 200)
	if own.Total != 3 {
		t.Fatal("assigned partner cannot see orders", own)
	}
	createUser(t, c, "unrelatedrider", store.DeliveryPartner)
	unrelated := loginAs(t, c, "unrelatedrider")
	own = must[store.List[store.Order]](t, unrelated, "GET", "/api/v1/orders", nil, 200)
	if own.Total != 0 {
		t.Fatal("other rider saw assigned orders")
	}
	wantError(t, unrelated, "GET", "/api/v1/trips/"+trip.TripID, nil, 403, "FORBIDDEN")
	// Assigned orders still require pickup before starting.
	wantError(t, rider, "POST", "/api/v1/trips/"+trip.TripID+"/start", nil, 409, "ORDER_NOT_FULLY_PICKED")
}

func TestMongoAssignmentToStartedTripAndForeignReferences(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	trip, first := f.trip(t, f.customer.CustomerID, 1)
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/pickup", store.PickupInput{OrderID: first.OrderID, LocationID: f.location.LocationID, Items: first.Items}, 200)
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/start", nil, 200)
	late := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: first.Items}, 201)
	in := store.AssignOrderInput{DeliveryPartnerID: f.driver.UserID}
	assigned := must[store.Order](t, c, "POST", "/api/v1/orders/"+late.OrderID+"/assign", in, 200)
	if assigned.TripID != trip.TripID {
		t.Fatal("started trip not reused")
	}
	rider := loginAs(t, c, "driver")
	wantError(t, rider, "POST", "/api/v1/trips/"+trip.TripID+"/stops/"+late.OrderID+"/close", store.CloseStopInput{Delivered: late.Items}, 409, "ORDER_NOT_FULLY_PICKED")
	other, _ := signupBusiness(t, c, "assignforeign", "Other business")
	foreignFixture := setupFixture(t, other, "assignforeign")
	createUser(t, other, "foreignextra", store.DeliveryPartner)
	foreignRider := createUser(t, other, "foreignextra2", store.DeliveryPartner)
	open := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: first.Items}, 201)
	wantError(t, c, "POST", "/api/v1/orders/"+open.OrderID+"/assign", store.AssignOrderInput{DeliveryPartnerID: foreignRider.UserID}, 404, "NOT_FOUND")
	// Same local ID in another business still refers only to that business's data.
	foreignOrder := must[store.Order](t, other, "POST", "/api/v1/orders", store.OrderInput{CustomerID: foreignFixture.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: foreignFixture.variant.VariantID, Quantity: 1}}}, 201)
	code, data := other.call("GET", "/api/v1/orders", nil)
	var rows store.List[store.Order]
	if code != 200 || json.Unmarshal(data, &rows) != nil || rows.Total != 1 || rows.Items[0].OrderID != foreignOrder.OrderID || rows.Items[0].TripID != "" {
		t.Fatal("assignment crossed businesses", string(data))
	}
}
