package api

import (
	"testing"

	"harbor/internal/store"
)

func TestMongoEmptyLocationAssignmentAllowsBusinessPickup(t *testing.T) {
	for _, role := range []string{store.WarehouseStaff, store.DeliveryPartner} {
		t.Run(role, func(t *testing.T) {
			c := testBackend(t)
			f := setupFixture(t, c)
			warehouse := must[store.Warehouse](t, c, "POST", "/api/v1/warehouses", store.WarehouseInput{Name: "Second Warehouse"}, 201)
			second := must[store.Location](t, c, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: warehouse.WarehouseID, Name: "Second Freezer", Type: "freezer"}, 201)
			for _, id := range []string{f.location.LocationID, second.LocationID} {
				must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: id, VariantID: f.variant.VariantID, Quantity: 10}, 201)
			}
			trip, order := f.trip(t, f.customer.CustomerID, 5)
			user, username := f.driver, "driver"
			if role == store.WarehouseStaff {
				username = "unrestrictedstaff"
				user = createUser(t, c, username, role)
			}
			actor := loginAs(t, c, username)
			pickup := func(locationID string) store.PickupInput {
				return store.PickupInput{OrderID: order.OrderID, LocationID: locationID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}
			}
			path := "/api/v1/trips/" + trip.TripID + "/pickup"
			if role == store.WarehouseStaff {
				wantError(t, actor, "POST", path, pickup(second.LocationID), 403, "FORBIDDEN")
				must[store.Trip](t, c, "PUT", "/api/v1/trips/"+trip.TripID+"/staff", map[string]any{"warehouseStaffIds": []string{user.UserID}}, 200)
			}
			locations := must[store.List[store.Location]](t, actor, "GET", "/api/v1/locations?page=1&limit=1", nil, 200)
			if locations.Total != 2 || len(locations.Items) != 1 {
				t.Fatal("empty assignments hid locations", locations)
			}
			page2 := must[store.List[store.Location]](t, actor, "GET", "/api/v1/locations?page=2&limit=1", nil, 200)
			if len(page2.Items) != 1 || page2.Items[0].LocationID != second.LocationID {
				t.Fatal("location pagination failed", page2)
			}
			// The partner/staff needs no location assignment for either warehouse.
			must[store.PickupResult](t, actor, "POST", path, pickup(f.location.LocationID), 200)
			must[store.PickupResult](t, actor, "POST", path, pickup(second.LocationID), 200)
			must[store.User](t, c, "PUT", "/api/v1/users/"+user.UserID+"/locations", map[string]any{"locationIds": []string{f.location.LocationID}}, 200)
			wantError(t, actor, "POST", path, pickup(second.LocationID), 409, "LOCATION_NOT_ASSIGNED")
			if stockAt(t, c, second.LocationID, f.variant.VariantID) != 9 || stockAt(t, c, trip.VehicleID, f.variant.VariantID) != 2 {
				t.Fatal("denied pickup changed stock")
			}
			must[store.PickupResult](t, actor, "POST", path, pickup(f.location.LocationID), 200)
			// Clearing the list grants unrestricted storage pickup with the same JWT.
			must[store.User](t, c, "PUT", "/api/v1/users/"+user.UserID+"/locations", map[string]any{"locationIds": []string{}}, 200)
			must[store.PickupResult](t, actor, "POST", path, pickup(second.LocationID), 200)
			if role == store.WarehouseStaff {
				warehouses := must[store.List[store.Warehouse]](t, actor, "GET", "/api/v1/warehouses", nil, 200)
				if warehouses.Total != 2 {
					t.Fatal("storage warehouses hidden", warehouses)
				}
				stocks := must[store.List[store.Stock]](t, actor, "GET", "/api/v1/stocks", nil, 200)
				if stocks.Total != 2 {
					t.Fatal("unrestricted storage list leaked van stock or lost storage", stocks)
				}
				vanStocks := must[store.List[store.Stock]](t, actor, "GET", "/api/v1/stocks?locationId="+trip.VehicleID, nil, 200)
				if vanStocks.Total != 0 {
					t.Fatal("global stock filter exposed vehicle stock", vanStocks)
				}
			}
			other := createUser(t, c, "othertriprider", store.DeliveryPartner)
			vehicle := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Other trip van"}, 201)
			otherTrip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: vehicle.VehicleID, DeliveryPartnerID: other.UserID}, 201)
			wantError(t, actor, "POST", "/api/v1/trips/"+otherTrip.TripID+"/pickup", pickup(second.LocationID), 403, "FORBIDDEN")
			// Overlapping account-local IDs must still resolve only within this account.
			foreign, _ := signupBusiness(t, c, "pickupforeign", "Foreign business")
			foreignFixture := setupFixture(t, foreign, "pickupforeign")
			must[store.Location](t, foreign, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: foreignFixture.location.WarehouseID, Name: "Foreign Two", Type: "rack"}, 201)
			foreignOnly := must[store.Location](t, foreign, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: foreignFixture.location.WarehouseID, Name: "Foreign Three", Type: "rack"}, 201)
			must[store.Movement](t, foreign, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: foreignOnly.LocationID, VariantID: foreignFixture.variant.VariantID, Quantity: 7}, 201)
			wantError(t, actor, "POST", path, pickup(foreignOnly.LocationID), 404, "NOT_FOUND")
			if stockAt(t, foreign, foreignOnly.LocationID, foreignFixture.variant.VariantID) != 7 || stockAt(t, c, trip.VehicleID, f.variant.VariantID) != 4 {
				t.Fatal("foreign location pickup changed stock")
			}
			// The final valid unit still writes exactly one movement and empties the order.
			picked := must[store.PickupResult](t, actor, "POST", path, pickup(second.LocationID), 200)
			if len(picked.Movements) != 1 || picked.Order.Picked[0].Quantity != 5 {
				t.Fatal("invalid final pickup", picked)
			}
			wantError(t, actor, "POST", path, pickup(trip.VehicleID), 404, "NOT_FOUND")
		})
	}
}
