package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"harbor/internal/config"
)

type DB struct {
	Client *mongo.Client
	Mongo  *mongo.Database
}

func Open(ctx context.Context, c config.Config) (*DB, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(c.MongoURI).SetServerSelectionTimeout(8*time.Second))
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*DB, error) { _ = client.Disconnect(context.Background()); return nil, err }
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return fail(err)
	}
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.M{"hello": 1}).Decode(&hello); err != nil {
		return fail(err)
	}
	if hello.SetName == "" && hello.Msg != "isdbgrid" {
		return fail(fmt.Errorf("MongoDB must be a replica set or sharded cluster: transactions are required"))
	}
	return &DB{client, client.Database(c.MongoDatabase)}, nil
}

func Connect(ctx context.Context, c config.Config) (*DB, error) {
	db, err := Open(ctx, c)
	if err != nil {
		return nil, err
	}
	if err := db.CheckLegacy(ctx); err != nil {
		_ = db.Client.Disconnect(ctx)
		return nil, err
	}
	if err := db.EnsureIndexes(ctx); err != nil {
		_ = db.Client.Disconnect(ctx)
		return nil, err
	}
	return db, nil
}

var BusinessIDs = map[string]string{"coupons": "couponId", "products": "productId", "variants": "variantId", "warehouses": "warehouseId", "locations": "locationId", "vehicles": "vehicleId", "customers": "customerId", "orders": "orderId", "trips": "tripId", "invoices": "invoiceId", "payments": "paymentId", "stockMovements": "movementId", "users": "userId", "counters": "counterName", "stocks": "stockKey", "paymentAllocations": "allocationKey", "settlements": "tripId", "partnerCashEntries": "entryId"}

func LegacyFilter() bson.M {
	return bson.M{"$or": bson.A{bson.M{"accountId": bson.M{"$exists": false}}, bson.M{"accountId": ""}, bson.M{"accountId": nil}}}
}
func (d *DB) CheckLegacy(ctx context.Context) error {
	for col := range BusinessIDs {
		n, err := d.Mongo.Collection(col).CountDocuments(ctx, LegacyFilter(), options.Count().SetLimit(1))
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("legacy data has no business ownership; stop the old application and run go run ./cmd/migrate-account -business-name <name> before starting this version")
		}
	}
	return nil
}

func (d *DB) WithTx(ctx context.Context, fn func(mongo.SessionContext) error) error {
	session, err := d.Client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	opts := options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary())
	// First use of a newly named counter or stock row can race on its unique
	// index. Retry the whole aborted transaction, never an individual write.
	for attempt := 0; attempt < 5; attempt++ {
		_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) { return nil, fn(sc) }, opts)
		if !mongo.IsDuplicateKeyError(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}

func (d *DB) PrepareIdentityIndexes(ctx context.Context) error {
	for collection, field := range map[string]string{"accounts": "accountId", "platformCounters": "counterName"} {
		_, err := d.Mongo.Collection(collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: field, Value: 1}}, Options: options.Index().SetUnique(true).SetName(field + "_1")})
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) EnsureIndexes(ctx context.Context) error {
	unique := func(col string, keys bson.D, name string, partial bson.M) error {
		opts := options.Index().SetUnique(true).SetName(name)
		if partial != nil {
			opts.SetPartialFilterExpression(partial)
		}
		_, err := d.Mongo.Collection(col).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: keys, Options: opts})
		return err
	}
	if err := unique("accounts", bson.D{{Key: "accountId", Value: 1}}, "accountId_1", nil); err != nil {
		return err
	}
	if err := unique("platformCounters", bson.D{{Key: "counterName", Value: 1}}, "counterName_1", nil); err != nil {
		return err
	}
	for col, field := range BusinessIDs {
		if err := unique(col, bson.D{{Key: "accountId", Value: 1}, {Key: field, Value: 1}}, "account_"+field, nil); err != nil {
			return err
		}
	}
	if err := unique("coupons", bson.D{{Key: "accountId", Value: 1}, {Key: "code", Value: 1}}, "account_coupon_code", nil); err != nil {
		return err
	}
	if err := unique("users", bson.D{{Key: "username", Value: 1}}, "username_1", nil); err != nil {
		return err
	}
	if err := unique("users", bson.D{{Key: "email", Value: 1}}, "email_unique", bson.M{"email": bson.M{"$type": "string"}}); err != nil {
		return err
	}
	if err := unique("stocks", bson.D{{Key: "accountId", Value: 1}, {Key: "locationId", Value: 1}, {Key: "variantId", Value: 1}}, "account_stock", nil); err != nil {
		return err
	}
	if err := unique("partnerCashEntries", bson.D{{Key: "accountId", Value: 1}, {Key: "requestId", Value: 1}}, "account_cash_request", nil); err != nil {
		return err
	}
	if err := unique("invoices", bson.D{{Key: "accountId", Value: 1}, {Key: "invoiceNo", Value: 1}}, "account_invoiceNo", nil); err != nil {
		return err
	}
	if err := unique("trips", bson.D{{Key: "accountId", Value: 1}, {Key: "vehicleId", Value: 1}}, "account_vehicle_reserved", bson.M{"vehicleReserved": true}); err != nil {
		return err
	}
	for col, fields := range map[string][]string{"invoices": {"customerId", "createdAt"}, "payments": {"customerId", "tripId", "createdAt"}, "paymentAllocations": {"customerId"}, "trips": {"deliveryPartnerId", "warehouseStaffIds", "createdAt"}, "stockMovements": {"tripId", "createdAt"}} {
		for _, field := range fields {
			_, err := d.Mongo.Collection(col).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "accountId", Value: 1}, {Key: field, Value: 1}}})
			if err != nil {
				return err
			}
		}
	}
	// Only retire the known old global constraints AFTER scoped replacements
	// exist. Document identifiers and all business records are retained.
	for col, field := range BusinessIDs {
		_, err := d.Mongo.Collection(col).Indexes().DropOne(ctx, field+"_1")
		if err != nil {
			var ce mongo.CommandError
			if !errors.As(err, &ce) || (ce.Code != 27 && ce.Code != 26) {
				return err
			}
		}
	}
	for col, names := range map[string][]string{"stocks": {"locationId_1_variantId_1"}, "invoices": {"invoiceNo_1"}, "trips": {"vehicleId_1"}} {
		for _, name := range names {
			_, err := d.Mongo.Collection(col).Indexes().DropOne(ctx, name)
			if err != nil {
				var ce mongo.CommandError
				if !errors.As(err, &ce) || (ce.Code != 27 && ce.Code != 26) {
					return err
				}
			}
		}
	}
	return nil
}
