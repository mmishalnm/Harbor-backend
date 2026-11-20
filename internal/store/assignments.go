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

func constrainLocationFilter(filter bson.M, allowed []string) {
	constrainIDFilter(filter, "locationId", allowed)
}
func constrainIDFilter(filter bson.M, key string, allowed []string) {
	if id, ok := filter[key].(string); ok {
		for _, candidate := range allowed {
			if candidate == id {
				return
			}
		}
		filter[key] = bson.M{"$in": []string{}}
		return
	}
	filter[key] = bson.M{"$in": allowed}
}

func (s *Store) restrictAssignedLists(ctx context.Context, a Actor, resource string, filter bson.M) error {
	if a.Role == WarehouseStaff || a.Role == DeliveryPartner {
		switch resource {
		case "locations":
			if len(a.LocationIDs) > 0 {
				constrainLocationFilter(filter, a.LocationIDs)
			}
		case "stocks":
			locationIDs := a.LocationIDs
			if len(locationIDs) == 0 {
				// Unrestricted storage access must not expose other trips' van stock.
				ids, err := s.col("locations").Distinct(ctx, "locationId", bson.M{})
				if err != nil {
					return err
				}
				locationIDs = []string{}
				for _, id := range ids {
					if value, ok := id.(string); ok {
						locationIDs = append(locationIDs, value)
					}
				}
			}
			if id, ok := filter["locationId"].(string); ok {
				allowed := false
				for _, assigned := range locationIDs {
					if assigned == id {
						allowed = true
					}
				}
				if !allowed {
					filter["locationId"] = bson.M{"$in": []string{}}
				}
			} else {
				filter["locationId"] = bson.M{"$in": locationIDs}
			}
		case "warehouses":
			if len(a.LocationIDs) == 0 {
				break
			}
			ids, err := s.col("locations").Distinct(ctx, "warehouseId", bson.M{"locationId": bson.M{"$in": a.LocationIDs}})
			if err != nil {
				return err
			}
			allowed := []string{}
			for _, id := range ids {
				if value, ok := id.(string); ok {
					allowed = append(allowed, value)
				}
			}
			constrainIDFilter(filter, "warehouseId", allowed)
		case "trips":
			if a.Role != WarehouseStaff {
				break
			}
			filter["warehouseStaffIds"] = a.UserID
		case "orders":
			if a.Role != WarehouseStaff {
				break
			}
			ids, err := s.col("trips").Distinct(ctx, "tripId", bson.M{"warehouseStaffIds": a.UserID})
			if err != nil {
				return err
			}
			pickup := bson.M{"fulfillmentType": CustomerPickupFulfillment}
			if len(a.LocationIDs) > 0 {
				pickup["pickupLocationId"] = bson.M{"$in": a.LocationIDs}
			}
			filter["$or"] = bson.A{bson.M{"createdBy": a.UserID}, bson.M{"tripId": bson.M{"$in": ids}}, pickup}
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
		allowed := []string{}
		for _, id := range ids {
			if value, ok := id.(string); ok {
				allowed = append(allowed, value)
			}
		}
		constrainIDFilter(filter, "vehicleId", allowed)
	}
	return nil
}
