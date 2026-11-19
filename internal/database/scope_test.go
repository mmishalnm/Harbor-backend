package database

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestAccountScopeFailsClosed(t *testing.T) {
	for _, id := range []string{"", "other", "ACC-1", "ACC-000001/other"} {
		ctx := WithAccount(context.Background(), id)
		if _, err := scopedFilter(ctx, bson.M{}); err == nil {
			t.Fatal("invalid account accepted", id)
		}
	}
	ctx := WithAccount(context.Background(), "ACC-000001")
	f, err := scopedFilter(ctx, bson.M{"$or": bson.A{bson.M{"accountId": "ACC-000002"}, bson.M{"name": "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	parts := f["$and"].(bson.A)
	if parts[0].(bson.M)["accountId"] != "ACC-000001" || len(parts) != 2 {
		t.Fatal("tenant constraint was replaced")
	}
	if _, err := scopedDocument(ctx, bson.M{"accountId": "ACC-000002"}); err == nil {
		t.Fatal("foreign insertion accepted")
	}
	if _, err := scopedUpdate(ctx, bson.M{"$set": bson.M{"accountId": "ACC-000002"}}); err == nil {
		t.Fatal("ownership change accepted")
	}
	if _, err := scopedUpdate(ctx, bson.M{"$rename": bson.M{"name": "accountId"}}); err == nil {
		t.Fatal("ownership rename accepted")
	}
	doc, err := scopedDocument(ctx, bson.M{"name": "owned"})
	if err != nil || doc["accountId"] != "ACC-000001" {
		t.Fatal(doc, err)
	}
	update, err := scopedUpdate(ctx, bson.M{"$inc": bson.M{"quantity": int64(1)}})
	if err != nil || update["$setOnInsert"].(bson.M)["accountId"] != "ACC-000001" {
		t.Fatal("upsert ownership was not enforced", err)
	}
}
