package store

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
)

// move is the ONLY stock mutation path. Its session always comes from WithTx.
// A guarded debit and append-only movement commit together with the credit.
func (s *Store) move(sc mongo.SessionContext, a Actor, variantID string, quantity int64, from, to, kind, ref, tripID, reason string) (Movement, error) {
	var out Movement
	if err := validItems([]Item{{variantID, quantity}}, false); err != nil {
		return out, err
	}
	if from == to || (from == "" && to == "") {
		return out, apperr.Validation("Stock movement needs distinct source and destination")
	}
	if from != "" {
		r, err := s.col("stocks").UpdateOne(sc, bson.M{"locationId": from, "variantId": variantID, "quantity": bson.M{"$gte": quantity}}, bson.M{"$inc": bson.M{"quantity": -quantity}})
		if err != nil {
			return out, err
		}
		if r.MatchedCount == 0 {
			return out, apperr.Conflict("INSUFFICIENT_STOCK", "Source location has insufficient stock")
		}
	}
	if to != "" {
		_, err := s.col("stocks").UpdateOne(sc, bson.M{"locationId": to, "variantId": variantID}, bson.M{"$inc": bson.M{"quantity": quantity}, "$setOnInsert": bson.M{"stockKey": to + ":" + variantID}}, options.Update().SetUpsert(true))
		if err != nil {
			return out, err
		}
	}
	id, err := s.next(sc, "movement", "MOV-")
	if err != nil {
		return out, err
	}
	out = Movement{id, variantID, quantity, from, to, kind, ref, tripID, reason, a.UserID, s.now().UTC()}
	return out, insert(sc, s.col("stockMovements"), out)
}

type ReceiveInput struct {
	LocationID string `json:"locationId"`
	VariantID  string `json:"variantId"`
	Quantity   int64  `json:"quantity"`
	Reason     string `json:"reason"`
}

func (s *Store) Receive(ctx context.Context, a Actor, in ReceiveInput) (Movement, error) {
	var out Movement
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager, WarehouseStaff}, func(sc mongo.SessionContext, a Actor) error {
		if _, err := s.storage(sc, in.LocationID); err != nil {
			return err
		}
		if err := checkPickup(a, in.LocationID); err != nil {
			return err
		}
		var v Variant
		if err := s.lock(sc, "variants", "variantId", in.VariantID, &v); err != nil {
			return err
		}
		var err error
		out, err = s.move(sc, a, in.VariantID, in.Quantity, "", in.LocationID, "receive", "", "", in.Reason)
		return err
	})
	return out, err
}

type TransferInput struct {
	FromLocationID string `json:"fromLocationId"`
	ToLocationID   string `json:"toLocationId"`
	VariantID      string `json:"variantId"`
	Quantity       int64  `json:"quantity"`
	Reason         string `json:"reason"`
}

func (s *Store) Transfer(ctx context.Context, a Actor, in TransferInput) (Movement, error) {
	var out Movement
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager, WarehouseStaff}, func(sc mongo.SessionContext, a Actor) error {
		if _, err := s.storage(sc, in.FromLocationID); err != nil {
			return err
		}
		if _, err := s.storage(sc, in.ToLocationID); err != nil {
			return err
		}
		if err := checkPickup(a, in.FromLocationID); err != nil {
			return err
		}
		var v Variant
		if err := s.lock(sc, "variants", "variantId", in.VariantID, &v); err != nil {
			return err
		}
		var err error
		out, err = s.move(sc, a, in.VariantID, in.Quantity, in.FromLocationID, in.ToLocationID, "transfer", "", "", in.Reason)
		return err
	})
	return out, err
}

type AdjustmentInput struct {
	LocationID    string `json:"locationId"`
	VariantID     string `json:"variantId"`
	QuantityDelta int64  `json:"quantityDelta"`
	Reason        string `json:"reason"`
}

func (s *Store) Adjust(ctx context.Context, a Actor, in AdjustmentInput) (Movement, error) {
	var out Movement
	if in.QuantityDelta == 0 || in.QuantityDelta > 1000000 || in.QuantityDelta < -1000000 {
		return out, apperr.Validation("quantityDelta must be nonzero and between -1,000,000 and 1,000,000")
	}
	if in.Reason == "" {
		return out, apperr.Validation("An adjustment reason is required")
	}
	if err := validText(in.Reason); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, a Actor) error {
		if _, err := s.storage(sc, in.LocationID); err != nil {
			return err
		}
		var v Variant
		if err := s.lock(sc, "variants", "variantId", in.VariantID, &v); err != nil {
			return err
		}
		from, to, q := "", in.LocationID, in.QuantityDelta
		if q < 0 {
			from, to, q = in.LocationID, "", -q
		}
		var err error
		out, err = s.move(sc, a, in.VariantID, q, from, to, "adjustment", "", "", in.Reason)
		return err
	})
	return out, err
}
