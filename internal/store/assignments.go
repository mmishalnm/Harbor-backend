package store

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
)

func (s *Store) validateTripStaff(sc mongo.SessionContext, ids []string) error {
	if len(ids) > 200 {
		return apperr.Validation("At most 200 warehouse staff can be assigned")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return apperr.Validation("Duplicate warehouseStaffId")
		}
		seen[id] = true
		var user User
		if err := s.lock(sc, "users", "userId", id, &user); err != nil {
			return err
		}
		if user.Role != WarehouseStaff {
			return apperr.Validation("warehouseStaffIds must identify warehouseStaff users in this business")
		}
	}
	return nil
}
func (s *Store) AssignTripStaff(ctx context.Context, a Actor, tripID string, ids []string) (Trip, error) {
	var out Trip
	if ids == nil {
		return out, apperr.Validation("warehouseStaffIds must be an array; use [] to clear it")
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, _ Actor) error {
		if err := s.lock(sc, "trips", "tripId", tripID, &out); err != nil {
			return err
		}
		if out.Status != "planned" {
			return apperr.Conflict("TRIP_NOT_PLANNED", "Change pickup staff before the trip starts")
		}
		if err := s.validateTripStaff(sc, ids); err != nil {
			return err
		}
		if err := s.update(sc, "trips", "tripId", tripID, bson.M{"warehouseStaffIds": ids}); err != nil {
			return err
		}
		return s.find(sc, "trips", "tripId", tripID, &out)
	})
	return out, err
}

func (s *Store) restrictAssignedLists(ctx context.Context, a Actor, resource string, filter bson.M) error {
	if a.LocationIDs == nil {
		a.LocationIDs = []string{}
	}
	if a.Role == WarehouseStaff {
		switch resource {
		case "locations":
			filter["locationId"] = bson.M{"$in": a.LocationIDs}
		case "stocks":
			if id, ok := filter["locationId"].(string); ok {
				allowed := false
				for _, assigned := range a.LocationIDs {
					if assigned == id {
						allowed = true
					}
				}
				if !allowed {
					filter["locationId"] = bson.M{"$in": []string{}}
				}
			} else {
				filter["locationId"] = bson.M{"$in": a.LocationIDs}
			}
		case "warehouses":
			ids, err := s.col("locations").Distinct(ctx, "warehouseId", bson.M{"locationId": bson.M{"$in": a.LocationIDs}})
			if err != nil {
				return err
			}
			filter["warehouseId"] = bson.M{"$in": ids}
		case "trips":
			filter["warehouseStaffIds"] = a.UserID
		case "orders":
			ids, err := s.col("trips").Distinct(ctx, "tripId", bson.M{"warehouseStaffIds": a.UserID})
			if err != nil {
				return err
			}
			filter["$or"] = bson.A{bson.M{"createdBy": a.UserID}, bson.M{"tripId": bson.M{"$in": ids}}}
		}
	}
	if resource == "orders" && a.Role == DeliveryPartner {
		ids, err := s.col("trips").Distinct(ctx, "tripId", bson.M{"deliveryPartnerId": a.UserID})
		if err != nil {
			return err
		}
		filter["$or"] = bson.A{bson.M{"createdBy": a.UserID}, bson.M{"tripId": bson.M{"$in": ids}}}
	}
	if resource == "vehicles" && (a.Role == WarehouseStaff || a.Role == DeliveryPartner) {
		assigned := bson.M{"warehouseStaffIds": a.UserID}
		if a.Role == DeliveryPartner {
			assigned = bson.M{"deliveryPartnerId": a.UserID}
		}
		ids, err := s.col("trips").Distinct(ctx, "vehicleId", assigned)
		if err != nil {
			return err
		}
		if a.Role == DeliveryPartner && a.AssignedVehicleID != "" {
			ids = append(ids, a.AssignedVehicleID)
		}
		filter["vehicleId"] = bson.M{"$in": ids}
	}
	return nil
}
