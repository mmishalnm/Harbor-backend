package api

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/database"
	"strings"
	"sync"
	"testing"
	"time"

	"harbor/internal/store"
)

func availability(t *testing.T, c testClient) store.AvailabilityList {
	t.Helper()
	return must[store.AvailabilityList](t, c, "GET", "/api/v1/inventory/availability?page=1&limit=1", nil, 200)
}
func reserveOrder(t *testing.T, c testClient, f fixture, q int64) store.Order {
	t.Helper()
	return must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: q}}}, 201)
}
func TestMongoReserve225Minus13AndBulkCancellation(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 225}, 201)
	order := reserveOrder(t, c, f, 13)
	if len(order.ReservedItems) != 1 || order.ReservedItems[0].Quantity != 13 || len(order.AwaitingStockItems) != 0 {
		t.Fatal("order reservation missing", order)
	}
	view := availability(t, c)
	if view.Totals.OnHandQuantity != 225 || view.Totals.ReservedQuantity != 13 || view.Totals.AvailableQuantity != 212 {
		t.Fatal(view)
	}
	stocks := must[store.List[store.Stock]](t, c, "GET", "/api/v1/stocks", nil, 200)
	if stocks.Items[0].Quantity != 225 || stocks.Items[0].ReservedQuantity != 13 || stocks.Items[0].AvailableQuantity != 212 {
		t.Fatal("physical/available stock conflated", stocks)
	}
	movements := must[store.List[store.Movement]](t, c, "GET", "/api/v1/stockMovements", nil, 200)
	if movements.Total != 1 {
		t.Fatal("reserving moved physical stock")
	}
	second := reserveOrder(t, c, f, 2)
	wantError(t, c, "POST", "/api/v1/orders/bulk-cancel", store.BulkCancelOrdersInput{OrderIDs: []string{order.OrderID, "ORD-999999"}, Reason: "Customer cancelled"}, 404, "NOT_FOUND")
	if availability(t, c).Totals.ReservedQuantity != 15 {
		t.Fatal("failed batch partially cancelled")
	}
	cancelled := must[store.BulkOrdersResult](t, c, "POST", "/api/v1/orders/bulk-cancel", store.BulkCancelOrdersInput{OrderIDs: []string{order.OrderID, second.OrderID}, Reason: "Customer cancelled"}, 200)
	if len(cancelled.Orders) != 2 || cancelled.Orders[0].CancelledBy == "" || cancelled.Orders[0].CancelledAt == nil {
		t.Fatal("missing cancellation audit", cancelled)
	}
	if got := availability(t, c).Totals; got.ReservedQuantity != 0 || got.AvailableQuantity != 225 || got.OnHandQuantity != 225 {
		t.Fatal("reservation not released", got)
	}
	other, _ := signupBusiness(t, c, "reservationother", "Other business")
	of := setupFixture(t, other, "reservationother")
	must[store.Movement](t, other, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: of.location.LocationID, VariantID: of.variant.VariantID, Quantity: 99}, 201)
	reserveOrder(t, other, of, 4)
	if availability(t, c).Totals.OnHandQuantity != 225 || availability(t, other).Totals.AvailableQuantity != 95 {
		t.Fatal("availability crossed businesses")
	}
	wantError(t, loginAs(t, c, "driver"), "GET", "/api/v1/inventory/availability", nil, 403, "FORBIDDEN")
	wantError(t, c, "GET", "/api/v1/inventory/availability?limit=201", nil, 400, "VALIDATION_ERROR")
}
func TestMongoReservationsProtectEarlierOrdersAndReceiveShortfall(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 5}, 201)
	old, newer := reserveOrder(t, c, f, 4), reserveOrder(t, c, f, 4)
	if len(newer.ReservedItems) != 1 || newer.ReservedItems[0].Quantity != 1 || newer.AwaitingStockItems[0].Quantity != 3 {
		t.Fatal("shortfall not shown", newer)
	}
	trips := []store.Trip{}
	for i, o := range []store.Order{old, newer} {
		v := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: []string{"Old van", "New van"}[i]}, 201)
		trips = append(trips, must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: v.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{o.OrderID}}, 201))
	}
	pick := func(trip store.Trip, o store.Order, q int64) store.PickupInput {
		return store.PickupInput{OrderID: o.OrderID, LocationID: f.location.LocationID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: q}}}
	}
	wantError(t, c, "POST", "/api/v1/trips/"+trips[1].TripID+"/pickup", pick(trips[1], newer, 2), 409, "INSUFFICIENT_STOCK")
	if stockAt(t, c, f.location.LocationID, f.variant.VariantID) != 5 {
		t.Fatal("late order stole reserved stock")
	}
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trips[0].TripID+"/pickup", pick(trips[0], old, 4), 200)
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trips[1].TripID+"/pickup", pick(trips[1], newer, 1), 200)
	if got := availability(t, c).Totals; got.OnHandQuantity != 0 || got.ReservedQuantity != 0 || got.AwaitingStockQuantity != 3 {
		t.Fatal("picked goods reserved twice", got)
	}
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 3}, 201)
	if got := availability(t, c).Totals; got.ReservedQuantity != 3 || got.AvailableQuantity != 0 || got.AwaitingStockQuantity != 0 {
		t.Fatal("receipt did not fill oldest shortfall", got)
	}
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trips[1].TripID+"/pickup", pick(trips[1], newer, 3), 200)
}
func TestMongoRiderSameDayTripsExplicitOrdersAndVehicleClaim(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Daily Van"}, 201)
	setRiderVehicle(t, c, f.driver.UserID, van.VehicleID)
	morning := must[store.Trip](t, rider, "POST", "/api/v1/trips", store.TripInput{Name: "Morning run"}, 201)
	afternoon := must[store.Trip](t, rider, "POST", "/api/v1/trips", store.TripInput{Name: "Afternoon run"}, 201)
	labels := must[store.DisplayNamesResult](t, rider, "POST", "/api/v1/display-names", store.DisplayNamesInput{IDs: []string{morning.TripID, afternoon.TripID}}, 200)
	if !strings.Contains(labels.Names[morning.TripID], "Morning run") || !strings.Contains(labels.Names[afternoon.TripID], "Afternoon run") || labels.Names[morning.TripID] == labels.Names[afternoon.TripID] {
		t.Fatal("same-day trips cannot be distinguished", labels)
	}
	if morning.TripID == afternoon.TripID || morning.TripDate != afternoon.TripDate || morning.DeliveryPartnerID != f.driver.UserID || morning.VehicleID != van.VehicleID {
		t.Fatal("same-day trip creation failed", morning, afternoon)
	}
	wantError(t, rider, "POST", "/api/v1/trips", store.TripInput{DeliveryPartnerID: "USR-000001"}, 403, "FORBIDDEN")
	wantError(t, rider, "POST", "/api/v1/trips", store.TripInput{StartDate: time.Now().AddDate(0, 0, 2).Format("2006-01-02")}, 400, "VALIDATION_ERROR")
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 3}, 201)
	in := store.OrderInput{CustomerID: f.customer.CustomerID, TripID: morning.TripID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 2}}}
	first := must[store.Order](t, rider, "POST", "/api/v1/orders", in, 201)
	in.TripID = afternoon.TripID
	in.Items[0].Quantity = 1
	second := must[store.Order](t, rider, "POST", "/api/v1/orders", in, 201)
	if first.TripID != morning.TripID || second.TripID != afternoon.TripID {
		t.Fatal("explicit trip ignored")
	}
	pick := func(o store.Order) store.PickupInput {
		return store.PickupInput{OrderID: o.OrderID, LocationID: f.location.LocationID, Items: o.Items}
	}
	must[store.PickupResult](t, rider, "POST", "/api/v1/trips/"+morning.TripID+"/pickup", pick(first), 200)
	wantError(t, rider, "POST", "/api/v1/trips/"+afternoon.TripID+"/pickup", pick(second), 409, "VEHICLE_BUSY")
	stock := must[store.List[store.Stock]](t, rider, "GET", "/api/v1/trips/"+afternoon.TripID+"/stock", nil, 200)
	if stock.Total != 0 {
		t.Fatal("unloaded trip exposed another trip's van stock")
	}
	must[store.Trip](t, rider, "POST", "/api/v1/trips/"+morning.TripID+"/start", nil, 200)
	must[store.CloseStopResult](t, rider, "POST", "/api/v1/trips/"+morning.TripID+"/stops/"+first.OrderID+"/close", store.CloseStopInput{Delivered: first.Items}, 200)
	must[store.Trip](t, rider, "POST", "/api/v1/trips/"+morning.TripID+"/complete", nil, 200)
	must[store.PickupResult](t, rider, "POST", "/api/v1/trips/"+afternoon.TripID+"/pickup", pick(second), 200)
	if stockAt(t, c, van.VehicleID, f.variant.VariantID) != 1 {
		t.Fatal("second trip did not claim empty van")
	}
}
func TestMongoBulkAssignmentIsAtomicAndRiderScoped(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	rider := loginAs(t, c, "driver")
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Bulk van"}, 201)
	setRiderVehicle(t, c, f.driver.UserID, van.VehicleID)
	trip := must[store.Trip](t, rider, "POST", "/api/v1/trips", store.TripInput{}, 201)
	in := store.OrderInput{CustomerID: f.customer.CustomerID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}
	one := must[store.Order](t, rider, "POST", "/api/v1/orders", in, 201)
	two := must[store.Order](t, rider, "POST", "/api/v1/orders", in, 201)
	office := reserveOrder(t, c, f, 1)
	wantError(t, rider, "POST", "/api/v1/orders/bulk-assign", store.BulkAssignOrdersInput{TripID: trip.TripID, OrderIDs: []string{one.OrderID, office.OrderID}}, 403, "FORBIDDEN")
	unchanged := must[store.Trip](t, c, "GET", "/api/v1/trips/"+trip.TripID, nil, 200)
	if len(unchanged.Stops) != 0 {
		t.Fatal("failed bulk assignment partially committed")
	}
	result := must[store.BulkOrdersResult](t, rider, "POST", "/api/v1/orders/bulk-assign", store.BulkAssignOrdersInput{TripID: trip.TripID, OrderIDs: []string{one.OrderID, two.OrderID}}, 200)
	if len(result.Orders) != 2 || result.TripID != trip.TripID {
		t.Fatal(result)
	}
	wantError(t, c, "POST", "/api/v1/orders/bulk-cancel", store.BulkCancelOrdersInput{OrderIDs: []string{office.OrderID, one.OrderID}, Reason: "Cancelled"}, 409, "ORDER_NOT_CANCELLABLE")
	orders := must[store.List[store.Order]](t, c, "GET", "/api/v1/orders", nil, 200)
	for _, o := range orders.Items {
		if o.OrderID == office.OrderID && o.Status != "open" {
			t.Fatal("failed cancellation partially committed")
		}
	}
	wantError(t, rider, "POST", "/api/v1/orders/bulk-cancel", store.BulkCancelOrdersInput{OrderIDs: []string{office.OrderID}, Reason: "No"}, 403, "FORBIDDEN")
	wantError(t, c, "POST", "/api/v1/orders/bulk-assign", store.BulkAssignOrdersInput{TripID: trip.TripID, OrderIDs: []string{office.OrderID, office.OrderID}}, 400, "VALIDATION_ERROR")
}
func TestMongoConcurrentOrdersCannotOverReserve(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 5}, 201)
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := c.call("POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 5}}})
			codes <- code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 201 {
			t.Fatal(code)
		}
	}
	if got := availability(t, c).Totals; got.ReservedQuantity != 5 || got.AvailableQuantity != 0 || got.AwaitingStockQuantity != 5 {
		t.Fatal("oversold physical stock", got)
	}
}

func TestMongoConcurrentTripsCannotLoadSameVehicle(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	secondRider := createUser(t, c, "claimrider", store.DeliveryPartner)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Shared planned van"}, 201)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 2}, 201)
	actors := []testClient{loginAs(t, c, "driver"), loginAs(t, c, "claimrider")}
	orders, trips := []store.Order{}, []store.Trip{}
	for _, u := range []store.User{f.driver, secondRider} {
		order := reserveOrder(t, c, f, 1)
		orders = append(orders, order)
		trips = append(trips, must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: u.UserID, OrderIDs: []string{order.OrderID}}, 201))
	}
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for i := range actors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, _ := actors[i].call("POST", "/api/v1/trips/"+trips[i].TripID+"/pickup", store.PickupInput{OrderID: orders[i].OrderID, LocationID: f.location.LocationID, Items: orders[i].Items})
			codes <- code
		}(i)
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
			t.Fatal(code)
		}
	}
	if success != 1 || conflict != 1 || stockAt(t, c, van.VehicleID, f.variant.VariantID) != 1 || stockAt(t, c, f.location.LocationID, f.variant.VariantID) != 1 {
		t.Fatal("van claimed by two trips", success, conflict)
	}
}

func TestMongoLegacyUnusedPlannedVanClaimCanBeReused(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Legacy planned van"}, 201)
	old := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID}, 201)
	profile := must[store.SessionProfile](t, c, "GET", "/api/v1/auth/me", nil, 200)
	ctx := database.WithAccount(context.Background(), profile.Account.AccountID)
	if err := c.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		_, err := c.db.Scoped("trips").UpdateOne(sc, bson.M{"tripId": old.TripID}, bson.M{"$set": bson.M{"vehicleReserved": true}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	order := reserveOrder(t, c, f, 1)
	next := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{order.OrderID}}, 201)
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+next.TripID+"/pickup", store.PickupInput{OrderID: order.OrderID, LocationID: f.location.LocationID, Items: order.Items}, 200)
	stock := must[store.List[store.Stock]](t, loginAs(t, c, "driver"), "GET", "/api/v1/trips/"+old.TripID+"/stock", nil, 200)
	if stock.Total != 0 {
		t.Fatal("old unused trip exposes newly loaded van", stock)
	}
}
