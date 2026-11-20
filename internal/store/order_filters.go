package store

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"harbor/internal/apperr"
)

// Dates are inclusive Indian calendar days, independent of server timezone.
func orderFilterConditions(filters map[string]string) (bson.M, error) {
	out := bson.M{}
	if mode := filters["fulfillmentType"]; mode != "" {
		if mode != DeliveryFulfillment && mode != CustomerPickupFulfillment {
			return nil, apperr.Validation("fulfillmentType must be delivery or customerPickup")
		}
		if mode == DeliveryFulfillment {
			out["fulfillmentType"] = bson.M{"$ne": CustomerPickupFulfillment}
		} else {
			out["fulfillmentType"] = mode
		}
	}
	if status := filters["status"]; status != "" {
		switch status {
		case "open", "assigned", "delivered", "partiallyDelivered", "failed", "cancelled", "collected", "partiallyCollected":
			out["status"] = status
		default:
			return nil, apperr.Validation("status must be open, assigned, delivered, partiallyDelivered, collected, partiallyCollected, failed, or cancelled")
		}
	}
	if source := filters["source"]; source != "" {
		switch source {
		case "counter", "salesStaff", "store", "phone", "pickup", "deliveryPartner":
			out["source"] = source
		default:
			return nil, apperr.Validation("source must be counter, salesStaff, store, phone, pickup, or deliveryPartner")
		}
	}
	dates, err := parseDateBounds(filters)
	if err != nil {
		return nil, err
	}
	if query := dates.query(); len(query) > 0 {
		out["createdAt"] = query
	}
	if id := filters["deliveryPartnerId"]; id != "" {
		if len(id) < 5 || len(id) > 64 || id[:4] != "USR-" || !displayID.MatchString(id) {
			return nil, apperr.Validation("deliveryPartnerId must be a user business ID")
		}
	}
	return out, nil
}
func (s *Store) applyOrderFilters(ctx context.Context, filters map[string]string, out bson.M) error {
	conditions, err := orderFilterConditions(filters)
	if err != nil {
		return err
	}
	for key, value := range conditions {
		out[key] = value
	}
	if id := filters["deliveryPartnerId"]; id != "" {
		ids, err := s.col("trips").Distinct(ctx, "tripId", bson.M{"deliveryPartnerId": id})
		if err != nil {
			return err
		}
		// Intersect with an explicit trip and assigned-role access; never replace it.
		out["$and"] = bson.A{bson.M{"tripId": bson.M{"$in": ids}}}
	}
	return nil
}
