package api

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"harbor/internal/store"
)

func setAutoTrips(t *testing.T, c testClient, enabled bool, days int) {
	t.Helper()
	must[store.OrderSettings](t, c, "PUT", "/api/v1/account/order-settings", map[string]any{"autoAssignDeliveryPartnerOrders": enabled, "tripDurationDays": days}, 200)
}
func setRiderVehicle(t *testing.T, c testClient, driverID, vehicleID string) {
	t.Helper()
	must[store.User](t, c, "PUT", "/api/v1/users/"+driverID+"/delivery-vehicle", map[string]string{"vehicleId": vehicleID}, 200)
}
func TestMongoAutomaticDailyTripCreationAndConcurrentOrders(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	driver := loginAs(t, c, "driver")
	settings := must[store.OrderSettings](t, driver, "GET", "/api/v1/account/order-settings", nil, 200)
	if settings.TripDurationDays != 1 || settings.AutoAssignDeliveryPartnerOrders {
		t.Fatal("default trip policy is incorrect", settings)
	}
	in := store.OrderInput{CustomerID: f.customer.CustomerID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}
	open := must[store.Order](t, driver, "POST", "/api/v1/orders", in, 201)
	if open.TripID != "" || open.Status != "open" || open.Source != "deliveryPartner" {
		t.Fatal("disabled policy should leave order open", open)
	}
	setAutoTrips(t, c, true, 1)
	wantError(t, driver, "POST", "/api/v1/orders", in, 409, "DELIVERY_VEHICLE_NOT_ASSIGNED")
	rows := must[store.List[store.Order]](t, c, "GET", "/api/v1/orders", nil, 200)
	if rows.Total != 1 {
		t.Fatal("failed assignment left an orphan order")
	}
	wantError(t, driver, "PUT", "/api/v1/account/order-settings", map[string]any{"autoAssignDeliveryPartnerOrders": false, "tripDurationDays": 1}, 403, "FORBIDDEN")
	vehicle := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Auto van"}, 201)
	setRiderVehicle(t, c, f.driver.UserID, vehicle.VehicleID)
	type response struct {
		code int
		data []byte
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, data := driver.call("POST", "/api/v1/orders", in)
			results <- response{code, data}
		}()
	}
	wg.Wait()
	close(results)
	tripID := ""
	for result := range results {
		if result.code != 201 {
			t.Fatalf("auto order failed %d %s", result.code, result.data)
		}
		var order store.Order
		if err := json.Unmarshal(result.data, &order); err != nil {
			t.Fatal(err)
		}
		if order.Status != "assigned" || order.TripID == "" {
			t.Fatal("order was not assigned", order)
		}
		if tripID != "" && tripID != order.TripID {
			t.Fatal("duplicate daily trips")
		}
		tripID = order.TripID
	}
	trips := must[store.List[store.Trip]](t, c, "GET", "/api/v1/trips", nil, 200)
	if trips.Total != 1 || len(trips.Items[0].Stops) != 2 || trips.Items[0].DurationDays != 1 || trips.Items[0].VehicleID != vehicle.VehicleID {
		t.Fatal("incorrect automatic trip", trips)
	}
	if stockAt(t, c, vehicle.VehicleID, f.variant.VariantID) != 0 {
		t.Fatal("creating an order changed van stock")
	}
	own := must[store.List[store.Order]](t, driver, "GET", "/api/v1/orders", nil, 200)
	if own.Total != 3 {
		t.Fatal("partner cannot see their own orders", own.Total)
	}
	other := createUser(t, c, "otherrider", store.DeliveryPartner)
	_ = other
	otherDriver := loginAs(t, c, "otherrider")
	others := must[store.List[store.Order]](t, otherDriver, "GET", "/api/v1/orders", nil, 200)
	if others.Total != 0 {
		t.Fatal("another rider's orders were exposed")
	}
	business, _ := signupBusiness(t, c, "independent", "Independent business")
	bf := setupFixture(t, business, "independent")
	bd := loginAs(t, business, "independentdriver")
	otherOrder := must[store.Order](t, bd, "POST", "/api/v1/orders", store.OrderInput{CustomerID: bf.customer.CustomerID, Items: []store.Item{{VariantID: bf.variant.VariantID, Quantity: 1}}}, 201)
	if otherOrder.TripID != "" {
		t.Fatal("one business's auto-assignment policy affected another")
	}
}

func TestMongoLateOrderUsesStartedTripAndRequiresPickup(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 10}, 201)
	trip, original := f.trip(t, f.customer.CustomerID, 1)
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/pickup", store.PickupInput{OrderID: original.OrderID, LocationID: f.location.LocationID, Items: original.Items}, 200)
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/start", nil, 200)
	setAutoTrips(t, c, true, 1)
	driver := loginAs(t, c, "driver")
	late := must[store.Order](t, driver, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Items: original.Items}, 201)
	if late.TripID != trip.TripID {
		t.Fatal("late order was not added to the rider's ongoing trip")
	}
	path := "/api/v1/trips/" + trip.TripID + "/stops/" + late.OrderID + "/close"
	wantError(t, driver, "POST", path, store.CloseStopInput{Delivered: late.Items}, 409, "ORDER_NOT_FULLY_PICKED")
	if stockAt(t, c, trip.VehicleID, f.variant.VariantID) != 1 {
		t.Fatal("unpicked late order consumed another order's stock")
	}
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/pickup", store.PickupInput{OrderID: late.OrderID, LocationID: f.location.LocationID, Items: late.Items}, 200)
	must[store.CloseStopResult](t, driver, "POST", path, store.CloseStopInput{Delivered: late.Items}, 200)
	if stockAt(t, c, trip.VehicleID, f.variant.VariantID) != 1 {
		t.Fatal("late delivery changed the original order's stock")
	}
}

func TestMongoExpiredTripAndAdminDuration(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 5}, 201)
	order := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}, 201)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Daily van"}, 201)
	yesterday := time.Now().In(time.FixedZone("IST", 19800)).AddDate(0, 0, -1).Format("2006-01-02")
	trip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{order.OrderID}, StartDate: yesterday}, 201)
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/pickup", store.PickupInput{OrderID: order.OrderID, LocationID: f.location.LocationID, Items: order.Items}, 200)
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/start", nil, 200)
	setRiderVehicle(t, c, f.driver.UserID, van.VehicleID)
	setAutoTrips(t, c, true, 3)
	driver := loginAs(t, c, "driver")
	in := store.OrderInput{CustomerID: f.customer.CustomerID, Items: order.Items}
	planned := must[store.Order](t, driver, "POST", "/api/v1/orders", in, 201)
	wantError(t, c, "POST", "/api/v1/trips/"+planned.TripID+"/pickup", store.PickupInput{OrderID: planned.OrderID, LocationID: f.location.LocationID, Items: planned.Items}, 409, "VEHICLE_BUSY")
	must[store.CloseStopResult](t, driver, "POST", "/api/v1/trips/"+trip.TripID+"/stops/"+order.OrderID+"/close", store.CloseStopInput{Delivered: order.Items}, 200)
	must[store.Trip](t, driver, "POST", "/api/v1/trips/"+trip.TripID+"/complete", nil, 200)
	created := must[store.Order](t, driver, "POST", "/api/v1/orders", in, 201)
	next := must[store.Trip](t, driver, "GET", "/api/v1/trips/"+created.TripID, nil, 200)
	if next.TripID == trip.TripID || next.DurationDays != 3 || next.EndsAt.Sub(next.StartsAt) != 72*time.Hour {
		t.Fatal("new trip did not use configured duration", next.TripSchedule)
	}
	old := must[store.Trip](t, driver, "GET", "/api/v1/trips/"+trip.TripID, nil, 200)
	if old.DurationDays != 1 {
		t.Fatal("changing settings changed an existing trip's duration")
	}
}
