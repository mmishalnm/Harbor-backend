package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/config"
	"harbor/internal/database"
)

type Store struct {
	db     *database.DB
	secret []byte
	now    func() time.Time
}

func New(db *database.DB, c config.Config) *Store { return &Store{db, []byte(c.JWTSecret), time.Now} }
func (s *Store) base() Base {
	now := s.now().UTC()
	return Base{IsActive: true, CreatedAt: now, UpdatedAt: now}
}
func (s *Store) col(name string) *database.Collection { return s.db.Scoped(name) }
func (s *Store) tx(ctx context.Context, a Actor, roles []string, fn func(mongo.SessionContext, Actor) error) error {
	ctx = database.WithAccount(ctx, a.AccountID)
	return s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		var account BusinessAccount
		if err := s.col("accounts").FindOne(sc, bson.M{"isActive": true}).Decode(&account); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return invalidAuth
			}
			return err
		}
		var u User
		if err := s.lock(sc, "users", "userId", a.UserID, &u); err != nil {
			if apperr.Public(err).Code == "NOT_FOUND" {
				return apperr.New(401, "UNAUTHORIZED", "Authentication is required")
			}
			return err
		}
		fresh := Actor{AssignedVehicleID: u.AssignedVehicleID, UserID: u.UserID, Role: u.Role, LocationIDs: u.LocationIDs, AccountID: u.AccountID}
		if err := allow(fresh, roles...); err != nil {
			return err
		}
		return fn(sc, fresh)
	})
}
func (s *Store) lock(ctx mongo.SessionContext, collection, field, id string, out any) error {
	return s.lockMatching(ctx, collection, field, id, true, out)
}

// Existing obligations can be reconciled after the customer is archived.
// New orders still use lock(), which requires an active customer.
func (s *Store) lockCustomerLedger(ctx mongo.SessionContext, id string, out *Customer) error {
	return s.lockMatching(ctx, "customers", "customerId", id, false, out)
}

func (s *Store) lockMatching(ctx mongo.SessionContext, collection, field, id string, active bool, out any) error {
	if id == "" {
		return apperr.Validation(field + " is required")
	}
	filter := bson.M{field: id}
	if active {
		filter["isActive"] = true
	}
	err := s.col(collection).FindOneAndUpdate(ctx, filter, bson.M{"$inc": bson.M{"revision": 1}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return apperr.NotFound()
	}
	if err == nil {
		normalizeRecord(out)
	}
	return err
}
func (s *Store) find(ctx context.Context, collection, field, id string, out any) error {
	err := s.col(collection).FindOne(ctx, bson.M{field: id}).Decode(out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return apperr.NotFound()
	}
	if err == nil {
		normalizeRecord(out)
	}
	return err
}
func (s *Store) next(ctx mongo.SessionContext, name, prefix string) (string, error) {
	n, err := s.sequence(ctx, name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%06d", prefix, n), nil
}
func (s *Store) sequence(ctx mongo.SessionContext, name string) (int64, error) {
	var c struct {
		Seq int64 `bson:"seq"`
	}
	err := s.col("counters").FindOneAndUpdate(ctx, bson.M{"counterName": name}, bson.M{"$inc": bson.M{"seq": 1}}, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&c)
	return c.Seq, err
}
func insert(ctx mongo.SessionContext, c *database.Collection, v any) error {
	_, err := c.InsertOne(ctx, v)
	return err
}
func (s *Store) update(ctx mongo.SessionContext, collection, field, id string, fields bson.M) error {
	fields["updatedAt"] = s.now().UTC()
	_, err := s.col(collection).UpdateOne(ctx, bson.M{field: id}, bson.M{"$set": fields})
	return err
}
func (s *Store) storage(ctx mongo.SessionContext, id string) (Location, error) {
	var l Location
	err := s.lock(ctx, "locations", "locationId", id, &l)
	if err != nil {
		return l, err
	}
	var w Warehouse
	err = s.lock(ctx, "warehouses", "warehouseId", l.WarehouseID, &w)
	return l, err
}
func checkPickup(a Actor, locationID string) error {
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return err
	}
	// An empty list means unrestricted storage access within the signed business.
	// storage() still validates the location and warehouse before this check.
	if a.Role == Admin || a.Role == Manager || len(a.LocationIDs) == 0 {
		return nil
	}
	for _, id := range a.LocationIDs {
		if id == locationID {
			return nil
		}
	}
	return apperr.Conflict("LOCATION_NOT_ASSIGNED", "Pickup is limited to your assigned locations")
}
func (s *Store) tripAccess(a Actor, t Trip) error {
	if a.Role == WarehouseStaff {
		assigned := false
		for _, id := range t.WarehouseStaffIDs {
			if id == a.UserID {
				assigned = true
			}
		}
		if !assigned {
			return apperr.Forbidden()
		}
	}
	if a.Role == DeliveryPartner && t.DeliveryPartnerID != a.UserID {
		return apperr.Forbidden()
	}
	return nil
}

func list[T any](ctx context.Context, s *Store, collection string, filter bson.M, p Page, sort bson.D) (List[T], error) {
	out := List[T]{Items: []T{}, Page: p.Page, Limit: p.Limit}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	total, err := s.col(collection).CountDocuments(ctx, filter)
	if err != nil {
		return out, err
	}
	out.Total = total
	cur, err := s.col(collection).Find(ctx, filter, options.Find().SetSort(sort).SetSkip(int64(p.Page-1)*int64(p.Limit)).SetLimit(int64(p.Limit)))
	if err != nil {
		return out, err
	}
	defer cur.Close(ctx)
	err = cur.All(ctx, &out.Items)
	return out, err
}

// ListResources returns typed models only; driver metadata cannot enter an API.
func (s *Store) ListResources(ctx context.Context, a Actor, resource string, p Page, filters map[string]string) (any, error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	f := bson.M{}
	sort := bson.D{{Key: "createdAt", Value: -1}}
	if err := ValidatePage(p); err != nil {
		return nil, err
	}
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return nil, err
	}
	for _, k := range []string{"warehouseId", "productId", "locationId", "variantId", "customerId", "tripId"} {
		if v := filters[k]; v != "" {
			f[k] = v
		}
	}
	if v := filters["isActive"]; v != "" {
		if v != "true" && v != "false" {
			return nil, apperr.Validation("isActive must be true or false")
		}
		f["isActive"] = v == "true"
	}
	if err := s.restrictAssignedLists(ctx, a, resource, f); err != nil {
		return nil, err
	}
	adminOnly := func() error { return allow(a, Admin, Manager) }
	switch resource {
	case "products":
		return list[Product](ctx, s, resource, f, p, bson.D{{Key: "productId", Value: 1}})
	case "variants":
		return list[Variant](ctx, s, resource, f, p, bson.D{{Key: "variantId", Value: 1}})
	case "warehouses":
		return list[Warehouse](ctx, s, resource, f, p, bson.D{{Key: "warehouseId", Value: 1}})
	case "locations":
		return list[Location](ctx, s, resource, f, p, bson.D{{Key: "locationId", Value: 1}})
	case "vehicles":
		return list[Vehicle](ctx, s, resource, f, p, bson.D{{Key: "vehicleId", Value: 1}})
	case "trips":
		if a.Role == DeliveryPartner {
			f["deliveryPartnerId"] = a.UserID
		}
		rows, err := list[Trip](ctx, s, resource, f, p, sort)
		for i := range rows.Items {
			normalizeTrip(&rows.Items[i])
		}
		return rows, err
	case "orders":
		return s.reservedOrders(ctx, f, p, sort)
	case "stocks":
		if a.Role == DeliveryPartner {
			return nil, apperr.Forbidden()
		}
		return s.reservedStocks(ctx, f, p)
	case "stockMovements":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[Movement](ctx, s, resource, f, p, sort)
	case "customers":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[Customer](ctx, s, resource, f, p, bson.D{{Key: "customerId", Value: 1}})
	case "users":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[User](ctx, s, resource, f, p, bson.D{{Key: "userId", Value: 1}})
	case "invoices":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[Invoice](ctx, s, resource, f, p, sort)
	case "payments":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[Payment](ctx, s, resource, f, p, sort)
	case "paymentAllocations":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[Allocation](ctx, s, resource, f, p, sort)
	case "settlements":
		if err := adminOnly(); err != nil {
			return nil, err
		}
		return list[Settlement](ctx, s, resource, f, p, sort)
	}
	return nil, apperr.NotFound()
}
func (s *Store) GetTrip(ctx context.Context, a Actor, id string) (Trip, error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	var t Trip
	if err := allow(a, Admin, Manager, WarehouseStaff, DeliveryPartner); err != nil {
		return t, err
	}
	if err := s.find(ctx, "trips", "tripId", id, &t); err != nil {
		return t, err
	}
	return t, s.tripAccess(a, t)
}
func (s *Store) GetInvoice(ctx context.Context, a Actor, id string) (Invoice, error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	var i Invoice
	if err := allow(a, Admin, Manager, DeliveryPartner); err != nil {
		return i, err
	}
	if err := s.find(ctx, "invoices", "invoiceId", id, &i); err != nil {
		return i, err
	}
	if a.Role == DeliveryPartner {
		var t Trip
		if err := s.find(ctx, "trips", "tripId", i.TripID, &t); err != nil {
			return i, err
		}
		if err := s.tripAccess(a, t); err != nil {
			return i, err
		}
	}
	return i, nil
}

func (s *Store) Archive(ctx context.Context, a Actor, resource, id string) error {
	fields := map[string]string{"products": "productId", "variants": "variantId", "customers": "customerId", "users": "userId"}
	field, ok := fields[resource]
	if !ok {
		return apperr.NotFound()
	}
	roles := []string{Admin, Manager}
	if resource == "users" {
		roles = []string{Admin}
	}
	return s.tx(ctx, a, roles, func(sc mongo.SessionContext, actor Actor) error {
		if resource == "variants" {
			var v Variant
			if err := s.lock(sc, resource, field, id, &v); err != nil {
				return err
			}
			n, err := s.col("stocks").CountDocuments(sc, bson.M{"variantId": id, "quantity": bson.M{"$ne": 0}})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("VARIANT_IN_USE", "Clear this variant's stock before archiving it")
			}
			n, err = s.col("orders").CountDocuments(sc, bson.M{"items.variantId": id, "status": bson.M{"$in": []string{"open", "assigned"}}})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("VARIANT_IN_USE", "Finish this variant's open orders before archiving it")
			}
		}
		if resource == "users" && id == actor.UserID {
			return apperr.Validation("You cannot archive your own account")
		}
		if resource == "users" {
			var u User
			if err := s.lock(sc, resource, field, id, &u); err != nil {
				return err
			}
			if u.Role == DeliveryPartner {
				n, err := s.col("trips").CountDocuments(sc, bson.M{"deliveryPartnerId": id, "status": bson.M{"$ne": "settled"}})
				if err != nil {
					return err
				}
				if n > 0 {
					return apperr.Conflict("USER_HAS_OPEN_TRIPS", "Settle assigned trips before archiving this user")
				}
			}
		}
		res, err := s.col(resource).UpdateOne(sc, bson.M{field: id, "isActive": true}, bson.M{"$set": bson.M{"isActive": false, "updatedAt": s.now().UTC()}, "$inc": bson.M{"revision": 1}})
		if err == nil && res.MatchedCount == 0 {
			return apperr.NotFound()
		}
		return err
	})
}

func normalizeRecord(out any) {
	switch v := out.(type) {
	case *Trip:
		normalizeTrip(v)
	case *BusinessAccount:
		v.OrderSettings = effectiveOrderSettings(v.OrderSettings)
	}
}
