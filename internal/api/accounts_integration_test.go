package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/database"
	"harbor/internal/store"
)

func signupBusiness(t *testing.T, c testClient, username, name string) (testClient, store.LoginResult) {
	t.Helper()
	input := store.SignupInput{Name: username, Username: username, Email: username + "@example.com", Phone: "9000000001", Password: "Integration-password-123", BusinessProfileInput: store.BusinessProfileInput{BusinessName: name, BusinessType: "Distribution", Address: store.BusinessAddress{Line1: "Main Road", City: "Thrissur", State: "Kerala", PostalCode: "680001", Country: "IN"}}}
	r := must[store.LoginResult](t, testClient{server: c.server}, "POST", "/api/v1/auth/signup", input, 201)
	if r.User.Role != store.Admin || r.User.AccountID == "" || r.User.AccountID != r.Account.AccountID || r.Account.BusinessName != name {
		t.Fatal("invalid business signup", r.Account)
	}
	return testClient{server: c.server, token: r.AccessToken, db: c.db}, r
}

func TestMongoConcurrentSignupIsAtomic(t *testing.T) {
	c := testBackend(t)
	in := store.SignupInput{Name: "Owner", Username: "concurrentowner", Email: "owner@example.com", Phone: "9000000001", Password: "Integration-password-123", BusinessProfileInput: store.BusinessProfileInput{BusinessName: "Concurrent business"}}
	type response struct {
		status int
		body   []byte
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, body := c.call("POST", "/api/v1/auth/signup", in)
			results <- response{status, body}
		}()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for r := range results {
		if r.status == 201 {
			ok++
		} else if r.status == 409 && strings.Contains(string(r.body), "LOGIN_EXISTS") {
			conflicts++
		} else {
			t.Fatalf("unexpected signup response %d %s", r.status, r.body)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal("duplicate signup did not close atomically")
	}
	count, err := c.db.Mongo.Collection("accounts").CountDocuments(context.Background(), bson.M{"businessName": "Concurrent business"})
	if err != nil || count != 1 {
		t.Fatal("orphan or duplicate account", count, err)
	}
	wantError(t, c, "POST", "/api/v1/auth/signup", map[string]any{"role": "warehouseStaff", "name": "Escalation"}, 400, "VALIDATION_ERROR")
}

func TestMongoLegacyBusinessMigration(t *testing.T) {
	c := testBackend(t)
	ctx := context.Background()
	var old store.User
	if err := c.db.Mongo.Collection("users").FindOne(ctx, bson.M{"username": "admin"}).Decode(&old); err != nil {
		t.Fatal(err)
	}
	old.AccountID = ""
	old.UserID = "USR-000009"
	old.Username = "legacyadmin"
	err := c.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		if _, err := c.db.Mongo.Collection("users").InsertOne(sc, old); err != nil {
			return err
		}
		for col, doc := range map[string]bson.M{
			"products":       {"productId": "MPN-000009", "name": "Legacy ICE", "isActive": true},
			"stocks":         {"stockKey": "LOC-000009:MVN-000009", "locationId": "LOC-000009", "variantId": "MVN-000009", "quantity": 5},
			"stockMovements": {"movementId": "MOV-000009", "quantity": 5, "variantId": "MVN-000009", "toLocationId": "LOC-000009", "kind": "receive"},
			"invoices":       {"invoiceId": "INV-000009", "invoiceNo": "INV/26-27/00009", "total": 120, "status": "issued", "isActive": true},
			"counters":       {"counterName": "product", "seq": int64(9)},
		} {
			if _, err := c.db.Mongo.Collection(col).InsertOne(sc, doc); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.db.CheckLegacy(ctx); err == nil {
		t.Fatal("startup did not reject ambiguous legacy ownership")
	}
	account, err := c.server.Store.MigrateLegacyAccount(ctx, "legacyadmin", "Existing Frozen Business")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.db.CheckLegacy(ctx); err != nil {
		t.Fatal(err)
	}
	legacy := loginAs(t, c, "legacyadmin")
	rows := must[store.List[store.Product]](t, legacy, "GET", "/api/v1/products", nil, 200)
	if rows.Total != 1 || rows.Items[0].ProductID != "MPN-000009" {
		t.Fatal("legacy products lost")
	}
	other := must[store.List[store.Product]](t, c, "GET", "/api/v1/products", nil, 200)
	if other.Total != 0 {
		t.Fatal("legacy records exposed to another business")
	}
	created := must[store.Product](t, legacy, "POST", "/api/v1/products", store.ProductInput{Name: "New product"}, 201)
	if created.ProductID != "MPN-000010" {
		t.Fatal("legacy counter reset")
	}
	var invoice store.Invoice
	if err := c.db.Mongo.Collection("invoices").FindOne(ctx, bson.M{"accountId": account.AccountID, "invoiceId": "INV-000009"}).Decode(&invoice); err != nil {
		t.Fatal(err)
	}
	if invoice.InvoiceNo != "INV/26-27/00009" || invoice.Total != 120 {
		t.Fatal("migration changed money or invoice number")
	}
}

func TestMongoBusinessSignupAndIsolation(t *testing.T) {
	root := testBackend(t)
	arun, arunSession := signupBusiness(t, root, "arun", "Arun Frozen Foods")
	varun, varunSession := signupBusiness(t, root, "varun", "Varun Stationery")
	if arunSession.Account.AccountID == varunSession.Account.AccountID {
		t.Fatal("businesses share an account")
	}
	af := setupFixture(t, arun, "arun")
	vf := setupFixture(t, varun, "varun")
	// Sequence IDs intentionally repeat between businesses. Ownership comes
	// from the signed account, so identical IDs still resolve independently.
	if af.variant.VariantID != vf.variant.VariantID {
		t.Fatal("expected independent per-business sequences")
	}
	must[store.Variant](t, varun, "PATCH", "/api/v1/variants/"+vf.variant.VariantID+"/price", map[string]float64{"price": 25}, 200)
	for _, c := range []testClient{arun, varun} {
		f := af
		quantity := int64(10)
		if c.token == varun.token {
			f = vf
			quantity = 30
		}
		must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: quantity}, 201)
	}
	if stockAt(t, arun, af.location.LocationID, af.variant.VariantID) != 10 || stockAt(t, varun, vf.location.LocationID, vf.variant.VariantID) != 30 {
		t.Fatal("stock crossed accounts")
	}
	ai, at := af.deliver(t, af.customer.CustomerID, 1, 200)
	vi, _ := vf.deliver(t, vf.customer.CustomerID, 1, 10)
	if ai.Invoice.Total != 120 || vi.Invoice.Total != 25 || ai.Invoice.InvoiceNo != vi.Invoice.InvoiceNo {
		t.Fatal("price/number series not isolated")
	}
	for _, tc := range []struct {
		client                    testClient
		invoice, payment, balance float64
	}{{arun, 120, 200, -80}, {varun, 25, 10, 15}} {
		ledger := must[store.Ledger](t, tc.client, "GET", "/api/v1/customers/"+af.customer.CustomerID+"/ledger", nil, 200)
		if ledger.InvoiceTotal != tc.invoice || ledger.PaymentTotal != tc.payment || ledger.Balance != tc.balance {
			t.Fatal("financial data crossed businesses", ledger)
		}
	}
	// A foreign-only ID is not visible or usable as a reference.
	foreign := must[store.Customer](t, varun, "POST", "/api/v1/customers", store.CustomerInput{Name: "Varun Private Customer"}, 201)
	wantError(t, arun, "GET", "/api/v1/customers/"+foreign.CustomerID+"/ledger", nil, 404, "NOT_FOUND")
	wantError(t, arun, "POST", "/api/v1/orders", store.OrderInput{CustomerID: foreign.CustomerID, Source: "phone", Items: []store.Item{{VariantID: af.variant.VariantID, Quantity: 1}}}, 404, "NOT_FOUND")
	foreignProduct := must[store.Product](t, varun, "POST", "/api/v1/products", store.ProductInput{Name: "Private stationery"}, 201)
	wantError(t, arun, "POST", "/api/v1/products/"+foreignProduct.ProductID+"/archive", nil, 404, "NOT_FOUND")
	for _, resource := range []string{"products", "variants", "warehouses", "locations", "customers", "users", "orders", "trips", "stocks", "stockMovements", "invoices", "payments", "paymentAllocations"} {
		status, data := arun.call("GET", "/api/v1/"+resource+"?accountId="+varunSession.Account.AccountID, nil)
		if status != 200 || strings.Contains(string(data), "Varun") || strings.Contains(string(data), varunSession.Account.AccountID) {
			t.Fatalf("foreign list data: %s %d %s", resource, status, data)
		}
	}
	must[store.SessionProfile](t, arun, "GET", "/api/v1/auth/me", nil, 200)
	account := must[store.BusinessAccount](t, arun, "GET", "/api/v1/account", nil, 200)
	if account.BusinessName != "Arun Frozen Foods" {
		t.Fatal("wrong business profile")
	}
	// An arbitrary client account header is never a source of ownership.
	claims := strings.Split(arun.token, ".")
	data, _ := base64.RawURLEncoding.DecodeString(claims[1])
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	body["accountId"] = varunSession.Account.AccountID
	changed, _ := json.Marshal(body)
	claims[1] = base64.RawURLEncoding.EncodeToString(changed)
	wantError(t, testClient{server: arun.server, token: strings.Join(claims, ".")}, "GET", "/api/v1/products", nil, 401, "UNAUTHORIZED")
	// Team members are account-owned and have assignment-restricted access.
	staff := createUser(t, arun, "arunstaff", store.WarehouseStaff)
	must[store.User](t, arun, "PUT", "/api/v1/users/"+staff.UserID+"/locations", map[string]any{"locationIds": []string{af.location.LocationID}}, 200)
	sc := loginAs(t, arun, "arunstaff")
	wantError(t, sc, "POST", "/api/v1/users", store.CreateUserInput{Name: "No", Username: "no", Password: "Integration-password-123", Role: store.WarehouseStaff}, 403, "FORBIDDEN")
	wantError(t, sc, "PUT", "/api/v1/account", store.BusinessProfileInput{BusinessName: "No"}, 403, "FORBIDDEN")
	wantError(t, arun, "POST", "/api/v1/users", store.CreateUserInput{Name: "Admin", Username: "anotheradmin", Password: "Integration-password-123", Role: store.Admin}, 400, "VALIDATION_ERROR")
	locations := must[store.List[store.Location]](t, sc, "GET", "/api/v1/locations", nil, 200)
	if locations.Total != 1 {
		t.Fatal("staff location visibility incorrect")
	}
	trips := must[store.List[store.Trip]](t, sc, "GET", "/api/v1/trips", nil, 200)
	if trips.Total != 0 {
		t.Fatal("unassigned trips visible")
	}
	wantError(t, sc, "GET", "/api/v1/trips/"+at.TripID, nil, 403, "FORBIDDEN")
	assigned, order := af.trip(t, af.customer.CustomerID, 1)
	must[store.Trip](t, arun, "PUT", "/api/v1/trips/"+assigned.TripID+"/staff", map[string]any{"warehouseStaffIds": []string{staff.UserID}}, 200)
	must[store.PickupResult](t, sc, "POST", "/api/v1/trips/"+assigned.TripID+"/pickup", store.PickupInput{OrderID: order.OrderID, LocationID: af.location.LocationID, Items: order.Items}, 200)
	rider := loginAs(t, arun, "arundriver")
	wantError(t, rider, "GET", "/api/v1/customers/"+af.customer.CustomerID+"/ledger", nil, 403, "FORBIDDEN")
	// Every stored business document has account ownership, not just reads.
	profile := must[store.SessionProfile](t, sc, "GET", "/api/v1/auth/me", nil, 200)
	if profile.User.AccountID != arunSession.Account.AccountID {
		t.Fatal("staff account not inherited")
	}
	for col := range database.BusinessIDs {
		n, err := arun.db.Mongo.Collection(col).CountDocuments(context.Background(), database.LegacyFilter())
		if err != nil || n != 0 {
			t.Fatalf("unscoped records in %s: %d %v", col, n, err)
		}
	}
}
