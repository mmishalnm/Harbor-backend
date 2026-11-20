package store

import (
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
)

func (s *Store) validateRiderTrip(sc mongo.SessionContext, a Actor, in *TripInput) error {
	if in.DeliveryPartnerID != "" && in.DeliveryPartnerID != a.UserID {
		return apperr.Forbidden()
	}
	if len(in.WarehouseStaffIDs) > 0 {
		return apperr.Forbidden()
	}
	in.DeliveryPartnerID = a.UserID
	if in.StartDate != "" && in.StartDate != s.now().In(tripTimezone).Format("2006-01-02") {
		return apperr.Validation("Delivery partners can create trips for today only")
	}
	if in.VehicleID == "" {
		in.VehicleID = a.AssignedVehicleID
	}
	if in.VehicleID == "" {
		return apperr.Conflict("DELIVERY_VEHICLE_NOT_ASSIGNED", "Admin must assign a vehicle before you can create a trip")
	}
	access := bson.M{}
	if err := s.restrictAssignedLists(sc, a, "vehicles", access); err != nil {
		return err
	}
	n, err := s.col("vehicles").CountDocuments(sc, bson.M{"$and": bson.A{access, bson.M{"vehicleId": in.VehicleID, "isActive": true}}})
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.Forbidden()
	}
	for _, id := range in.OrderIDs {
		var order Order
		if err := s.find(sc, "orders", "orderId", id, &order); err != nil {
			return err
		}
		if order.CreatedBy != a.UserID {
			return apperr.Forbidden()
		}
	}
	return nil
}

// Planned trips may share a van. Pickup claims the van until the loaded trip
// completes. The unique reservation index is retained as the final race guard.
func (s *Store) claimTripVehicle(sc mongo.SessionContext, t *Trip) error {
	var vehicle Vehicle
	if err := s.lock(sc, "vehicles", "vehicleId", t.VehicleID, &vehicle); err != nil {
		return err
	}
	if t.VehicleReserved {
		return nil
	}
	var other Trip
	err := s.col("trips").FindOne(sc, bson.M{"vehicleId": t.VehicleID, "vehicleReserved": true, "tripId": bson.M{"$ne": t.TripID}}).Decode(&other)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	if err == nil {
		// Older releases reserved an empty van at planning time. Release that
		// idle claim lazily, inside the same transaction, without moving stock.
		if other.Status != "planned" {
			return apperr.Conflict("VEHICLE_BUSY", "Finish the van's loaded trip before picking another trip")
		}
		var locked Trip
		if err := s.lock(sc, "trips", "tripId", other.TripID, &locked); err != nil {
			return err
		}
		picked, err := s.col("orders").CountDocuments(sc, bson.M{"tripId": other.TripID, "picked.0": bson.M{"$exists": true}})
		if err != nil {
			return err
		}
		if picked > 0 {
			return apperr.Conflict("VEHICLE_BUSY", "Finish the van's loaded trip before picking another trip")
		}
		if err := s.update(sc, "trips", "tripId", other.TripID, bson.M{"vehicleReserved": false}); err != nil {
			return err
		}
	}
	n, err := s.col("stocks").CountDocuments(sc, bson.M{"locationId": t.VehicleID, "quantity": bson.M{"$ne": 0}})
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflict("VAN_NOT_EMPTY", "Return or deliver the van's previous stock before loading a new trip")
	}
	if err := s.update(sc, "trips", "tripId", t.TripID, bson.M{"vehicleReserved": true}); err != nil {
		return err
	}
	t.VehicleReserved = true
	return nil
}

func (s *Store) attachOrderToTrip(sc mongo.SessionContext, a Actor, tripID string, order *Order) error {
	if err := requireDeliveryOrder(*order); err != nil {
		return err
	}
	var t Trip
	if err := s.lock(sc, "trips", "tripId", tripID, &t); err != nil {
		return err
	}
	if err := s.tripAccess(a, t); err != nil {
		return err
	}
	if a.Role == WarehouseStaff {
		return apperr.Forbidden()
	}
	if a.Role == DeliveryPartner && order.CreatedBy != a.UserID {
		return apperr.Forbidden()
	}
	if t.Status != "planned" && t.Status != "started" {
		return apperr.Conflict("TRIP_NOT_OPEN", "Choose a planned or started trip")
	}
	if order.Status != "open" || order.TripID != "" || len(order.Picked) > 0 {
		return apperr.Conflict("ORDER_ALREADY_ASSIGNED", "Only an open, unpicked order can be assigned")
	}
	if len(t.Stops) >= 200 {
		return apperr.Conflict("TRIP_FULL", "A trip supports at most 200 orders")
	}
	t.Stops = append(t.Stops, Stop{OrderID: order.OrderID, Status: "pending", Delivered: []Item{}})
	if err := s.update(sc, "trips", "tripId", t.TripID, bson.M{"stops": t.Stops}); err != nil {
		return err
	}
	if err := s.update(sc, "orders", "orderId", order.OrderID, bson.M{"tripId": t.TripID, "status": "assigned"}); err != nil {
		return err
	}
	order.TripID = t.TripID
	order.Status = "assigned"
	return nil
}
