package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"harbor/internal/store"
)

func testPlan(t *testing.T, c testClient, name string, skus, warehouses, partners int64, trial int) store.Plan {
	t.Helper()
	return must[store.Plan](t, loginAs(t, c, "platformowner"), "POST", "/api/v1/platform/plans", store.PlanInput{Name: name, SKULimit: skus, WarehouseLimit: warehouses, PartnerLimit: partners, TrialDays: trial, BillingInterval: "monthly"}, 201)
}
func rawBusiness(t *testing.T, c testClient, name string) testClient {
	t.Helper()
	r := must[store.LoginResult](t, c, "POST", "/api/v1/auth/signup", store.SignupInput{Name: name, Username: name, Email: name + "@example.com", Phone: "9000000001", Password: "Integration-password-123", BusinessProfileInput: store.BusinessProfileInput{BusinessName: name + " Business"}}, 201)
	return testClient{server: c.server, db: c.db, token: r.AccessToken}
}
func TestMongoPlatformPrivacyAndBusinessDeactivation(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	owner := loginAs(t, c, "platformowner")
	profile := must[store.SessionProfile](t, c, "GET", "/api/v1/auth/me", nil, 200)
	platformProfile := must[store.SessionProfile](t, owner, "GET", "/api/v1/auth/me", nil, 200)
	if platformProfile.User.Role != store.SuperAdmin || platformProfile.Account != nil {
		t.Fatal(platformProfile)
	}
	for _, route := range c.server.Routes {
		if len(route.Roles) == 0 || strings.HasPrefix(route.Path, "/api/v1/platform/") || route.Path == "/api/v1/auth/me" {
			continue
		}
		path := strings.ReplaceAll(strings.ReplaceAll(route.Path, "{id}", f.customer.CustomerID), "{orderId}", "ORD-000001")
		wantError(t, owner, route.Method, path, map[string]any{}, 403, "FORBIDDEN")
	}
	wantError(t, c, "GET", "/api/v1/platform/businesses", nil, 403, "FORBIDDEN")
	wantError(t, c, "POST", "/api/v1/platform/plans", store.PlanInput{}, 403, "FORBIDDEN")
	code, body := owner.call("GET", "/api/v1/platform/businesses", nil)
	if code != 200 {
		t.Fatal(code, string(body))
	}
	for _, secret := range []string{"Sunrise Stores", "Main Freezer", "passwordHash", "orderSettings", "customerId", "invoiceTotal", "salesAmount", "stocks"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("operational information leaked", secret)
		}
	}
	rows := must[store.List[store.BusinessRegistration]](t, owner, "GET", "/api/v1/platform/businesses", nil, 200)
	if rows.Total != 1 || rows.Items[0].Owner.Username != "admin" {
		t.Fatal(rows)
	}
	active := false
	path := "/api/v1/platform/businesses/" + profile.Account.AccountID + "/status"
	changed := must[store.BusinessRegistration](t, owner, "PUT", path, store.BusinessStatusInput{IsActive: &active, Reason: "Account review"}, 200)
	if changed.IsActive || changed.StatusReason != "Account review" {
		t.Fatal(changed)
	}
	wantError(t, c, "GET", "/api/v1/products", nil, 403, "BUSINESS_INACTIVE")
	wantError(t, loginAs(t, owner, "platformowner"), "PUT", path, store.BusinessStatusInput{IsActive: &active}, 400, "VALIDATION_ERROR")
	wantError(t, testClient{server: c.server}, "POST", "/api/v1/auth/login", map[string]string{"username": "driver", "password": "Integration-password-123"}, 403, "BUSINESS_INACTIVE")
	active = true
	must[store.BusinessRegistration](t, owner, "PUT", path, store.BusinessStatusInput{IsActive: &active, Reason: "Review completed"}, 200)
	products := must[store.List[store.Product]](t, c, "GET", "/api/v1/products", nil, 200)
	if products.Total != 1 {
		t.Fatal("data changed on deactivation", products)
	}
	events, err := c.db.Mongo.Collection("platformEvents").CountDocuments(context.Background(), bson.M{"referenceId": profile.Account.AccountID})
	if err != nil || events != 2 {
		t.Fatal(events, err)
	}
}
func TestMongoSubscriptionRequiredCapacityAndUpgrade(t *testing.T) {
	c := testBackend(t)
	tenant := rawBusiness(t, c, "newtenant")
	for _, method := range []string{"GET", "POST"} {
		wantError(t, tenant, method, "/api/v1/products", store.ProductInput{Name: "ICE"}, 402, "SUBSCRIPTION_REQUIRED")
	}
	me := must[store.SessionProfile](t, tenant, "GET", "/api/v1/auth/me", nil, 200)
	if me.Access == nil || me.Access.HasAccess || me.Access.Status != "required" {
		t.Fatal(me.Access)
	}
	plan := testPlan(t, c, "Starter", 2, 1, 1, 14)
	access := must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": plan.PlanID}, 200)
	if access.Status != "trialing" || !access.HasAccess || access.Plan.Price != 0 || access.TrialEndsAt == nil {
		t.Fatal(access)
	}
	f := setupFixture(t, tenant, "tenant")
	must[store.Variant](t, tenant, "POST", "/api/v1/variants", store.VariantInput{ProductID: f.variant.ProductID, Name: "2.5kg", Price: 65}, 201)
	wantError(t, tenant, "POST", "/api/v1/products", store.ProductInput{Name: "Another"}, 409, "PLAN_LIMIT_REACHED")
	wantError(t, tenant, "POST", "/api/v1/variants", store.VariantInput{ProductID: f.variant.ProductID, Name: "10kg", Price: 200}, 409, "PLAN_LIMIT_REACHED")
	wantError(t, tenant, "POST", "/api/v1/warehouses", store.WarehouseInput{Name: "Second warehouse"}, 409, "PLAN_LIMIT_REACHED")
	wantError(t, tenant, "POST", "/api/v1/users", store.CreateUserInput{Name: "Extra rider", Username: "extrarider", Password: "Integration-password-123", Role: store.DeliveryPartner}, 409, "PLAN_LIMIT_REACHED")
	small := testPlan(t, c, "Too small", 1, 1, 0, 30)
	wantError(t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": small.PlanID}, 409, "PLAN_BELOW_USAGE")
	larger := testPlan(t, c, "Growth", 4, 2, 2, 30)
	upgraded := must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": larger.PlanID}, 200)
	if !upgraded.TrialEndsAt.Equal(*access.TrialEndsAt) {
		t.Fatal("upgrade restarted trial", upgraded)
	}
	p := must[store.Product](t, tenant, "POST", "/api/v1/products", store.ProductInput{Name: "Notebook"}, 201)
	must[store.Variant](t, tenant, "POST", "/api/v1/variants", store.VariantInput{ProductID: p.ProductID, Name: "A4", Price: 50}, 201)
	status := must[store.SubscriptionAccess](t, tenant, "GET", "/api/v1/subscription", nil, 200)
	if status.Usage.SKUs != 3 {
		t.Fatal("first variant double counted", status.Usage)
	}
	createUser(t, tenant, "secondrider", store.DeliveryPartner)
	must[store.Warehouse](t, tenant, "POST", "/api/v1/warehouses", store.WarehouseInput{Name: "Second"}, 201)
	rider := loginAs(t, tenant, "secondrider")
	wantError(t, rider, "POST", "/api/v1/subscription", map[string]string{"planId": plan.PlanID}, 403, "FORBIDDEN")
	staffStatus := must[store.SubscriptionAccess](t, rider, "GET", "/api/v1/subscription", nil, 200)
	if staffStatus.Usage != nil {
		t.Fatal("staff usage disclosure")
	}
}
func TestMongoPlanSnapshotsCancellationAndExpiredGate(t *testing.T) {
	c := testBackend(t)
	tenant := rawBusiness(t, c, "trialtenant")
	owner := loginAs(t, c, "platformowner")
	plan := testPlan(t, c, "Trial plan", 10, 2, 2, 7)
	access := must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": plan.PlanID}, 200)
	changed := plan.PlanInput
	changed.SKULimit = 20
	changed.TrialDays = 100
	must[store.Plan](t, owner, "PUT", "/api/v1/platform/plans/"+plan.PlanID, changed, 200)
	must[map[string]bool](t, owner, "POST", "/api/v1/platform/plans/"+plan.PlanID+"/archive", nil, 200)
	status := must[store.SubscriptionAccess](t, tenant, "GET", "/api/v1/subscription", nil, 200)
	if status.Plan.SKULimit != 10 || !status.TrialEndsAt.Equal(*access.TrialEndsAt) {
		t.Fatal("accepted terms changed", status)
	}
	must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription/cancel", nil, 200)
	must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription/resume", nil, 200)
	cancelled := must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription/cancel", nil, 200)
	if cancelled.AutoRenew || !cancelled.HasAccess || !cancelled.PeriodEnd.Equal(*access.TrialEndsAt) {
		t.Fatal(cancelled)
	}
	me := must[store.SessionProfile](t, tenant, "GET", "/api/v1/auth/me", nil, 200)
	// Controlled clock fixture: emulate the end of an already-cancelled period.
	past := time.Now().UTC().Add(-time.Hour)
	_, err := c.db.Mongo.Collection("accounts").UpdateOne(context.Background(), bson.M{"accountId": me.Account.AccountID}, bson.M{"$set": bson.M{"subscription.accessEndsAt": past, "subscription.trialEndsAt": past}})
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, tenant, "GET", "/api/v1/products", nil, 402, "SUBSCRIPTION_EXPIRED")
	newPlan := testPlan(t, c, "Renewal", 10, 2, 2, 100)
	renewed := must[store.SubscriptionAccess](t, tenant, "POST", "/api/v1/subscription", map[string]string{"planId": newPlan.PlanID}, 200)
	if renewed.Status != "active" || renewed.TrialEndsAt != nil {
		t.Fatal("second trial granted", renewed)
	}
}
func TestMongoConcurrentTenantCapacity(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	_ = f
	plan := testPlan(t, c, "Two SKUs", 2, 1, 1, 0)
	must[store.SubscriptionAccess](t, c, "POST", "/api/v1/subscription", map[string]string{"planId": plan.PlanID}, 200)
	createUser(t, c, "capacitymanager", store.Manager)
	manager := loginAs(t, c, "capacitymanager")
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, actor := range []testClient{c, manager} {
		wg.Add(1)
		go func(actor testClient) {
			defer wg.Done()
			code, _ := actor.call("POST", "/api/v1/products", store.ProductInput{Name: "Concurrent product"})
			results <- code
		}(actor)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for code := range results {
		if code == 201 {
			success++
		} else if code == 409 {
			conflict++
		} else {
			t.Fatal(code)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
