package database

import (
	"context"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
)

type accountKey struct{}

var accountPattern = regexp.MustCompile(`^ACC-[0-9]{6,}$`)

func ValidAccountID(id string) bool { return accountPattern.MatchString(id) }
func WithAccount(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, accountKey{}, id)
}
func AccountID(ctx context.Context) (string, error) {
	id, _ := ctx.Value(accountKey{}).(string)
	if !ValidAccountID(id) {
		return "", apperr.New(401, "UNAUTHORIZED", "A valid business account is required")
	}
	return id, nil
}

// Collection fails closed without a server-provided account context. The
// account predicate is ANDed with the whole query, including any caller $or.
type Collection struct{ raw *mongo.Collection }

func (d *DB) Scoped(name string) *Collection { return &Collection{d.Mongo.Collection(name)} }
func scopedFilter(ctx context.Context, filter any) (bson.M, error) {
	id, err := AccountID(ctx)
	if err != nil {
		return nil, err
	}
	return bson.M{"$and": bson.A{bson.M{"accountId": id}, filter}}, nil
}
func scopedDocument(ctx context.Context, v any) (bson.M, error) {
	id, err := AccountID(ctx)
	if err != nil {
		return nil, err
	}
	data, err := bson.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc bson.M
	if err := bson.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if supplied, ok := doc["accountId"]; ok && supplied != "" && supplied != id {
		return nil, apperr.Forbidden()
	}
	doc["accountId"] = id
	return doc, nil
}
func scopedUpdate(ctx context.Context, v any) (bson.M, error) {
	id, err := AccountID(ctx)
	if err != nil {
		return nil, err
	}
	data, err := bson.Marshal(v)
	if err != nil {
		return nil, err
	}
	var update bson.M
	if err := bson.Unmarshal(data, &update); err != nil {
		return nil, err
	}
	for op, fields := range update {
		if op != "$set" && op != "$inc" && op != "$setOnInsert" {
			return nil, apperr.Validation("Unsupported update operation")
		}
		m, ok := fields.(bson.M)
		if !ok {
			return nil, apperr.Validation("Invalid update fields")
		}
		for key := range m {
			if key == "accountId" || strings.HasPrefix(key, "accountId.") {
				return nil, apperr.Forbidden()
			}
		}
	}
	onInsert, ok := update["$setOnInsert"].(bson.M)
	if !ok {
		onInsert = bson.M{}
	}
	onInsert["accountId"] = id
	update["$setOnInsert"] = onInsert
	return update, nil
}
func (c *Collection) FindOne(ctx context.Context, f any, opts ...*options.FindOneOptions) *mongo.SingleResult {
	query, err := scopedFilter(ctx, f)
	if err != nil {
		return mongo.NewSingleResultFromDocument(bson.M{}, err, nil)
	}
	return c.raw.FindOne(ctx, query, opts...)
}
func (c *Collection) Find(ctx context.Context, f any, opts ...*options.FindOptions) (*mongo.Cursor, error) {
	query, err := scopedFilter(ctx, f)
	if err != nil {
		return nil, err
	}
	return c.raw.Find(ctx, query, opts...)
}
func (c *Collection) CountDocuments(ctx context.Context, f any, opts ...*options.CountOptions) (int64, error) {
	query, err := scopedFilter(ctx, f)
	if err != nil {
		return 0, err
	}
	return c.raw.CountDocuments(ctx, query, opts...)
}
func (c *Collection) Distinct(ctx context.Context, field string, f any) ([]interface{}, error) {
	query, err := scopedFilter(ctx, f)
	if err != nil {
		return nil, err
	}
	return c.raw.Distinct(ctx, field, query)
}
func (c *Collection) InsertOne(ctx context.Context, v any) (*mongo.InsertOneResult, error) {
	doc, err := scopedDocument(ctx, v)
	if err != nil {
		return nil, err
	}
	return c.raw.InsertOne(ctx, doc)
}
func (c *Collection) UpdateOne(ctx context.Context, f, u any, opts ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	query, err := scopedFilter(ctx, f)
	if err != nil {
		return nil, err
	}
	update, err := scopedUpdate(ctx, u)
	if err != nil {
		return nil, err
	}
	return c.raw.UpdateOne(ctx, query, update, opts...)
}
func (c *Collection) FindOneAndUpdate(ctx context.Context, f, u any, opts ...*options.FindOneAndUpdateOptions) *mongo.SingleResult {
	query, err := scopedFilter(ctx, f)
	if err != nil {
		return mongo.NewSingleResultFromDocument(bson.M{}, err, nil)
	}
	update, err := scopedUpdate(ctx, u)
	if err != nil {
		return mongo.NewSingleResultFromDocument(bson.M{}, err, nil)
	}
	return c.raw.FindOneAndUpdate(ctx, query, update, opts...)
}
