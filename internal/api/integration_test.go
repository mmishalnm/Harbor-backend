package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"harbor/internal/config"
	"harbor/internal/database"
	"harbor/internal/store"
)

type testClient struct {
	db     *database.DB
	server *Server
	token  string
}

func (c testClient) call(method, path string, in any) (int, []byte) {
	var data []byte
	if in != nil {
		data, _ = json.Marshal(in)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	w := httptest.NewRecorder()
	c.server.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}
func must[T any](t *testing.T, c testClient, method, path string, in any, status int) T {
	t.Helper()
	code, data := c.call(method, path, in)
	if code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, code, status, data)
	}
	if bytes.Contains(data, []byte(`"_id"`)) || bytes.Contains(data, []byte("passwordHash")) {
		t.Fatal("private field in response")
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func wantError(t *testing.T, c testClient, method, path string, in any, status int, want string) {
	t.Helper()
	out := must[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, c, method, path, in, status)
	if out.Error.Code != want {
		t.Fatalf("error=%s want %s", out.Error.Code, want)
	}
}
func testBackend(t *testing.T) testClient {
	t.Helper()
	uri := os.Getenv("HARBOR_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set HARBOR_TEST_MONGO_URI to a disposable replica set to run MongoDB acceptance/concurrency tests")
	}
	c := config.Config{MongoURI: uri, MongoDatabase: fmt.Sprintf("harborTest_%d", time.Now().UnixNano()), JWTSecret: strings.Repeat("integration-secret-", 3)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Connect(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := db.Mongo.Drop(ctx); err != nil {
			t.Error(err)
		}
		_ = db.Client.Disconnect(ctx)
	})
	s := store.New(db, c)
	if err := s.Bootstrap(ctx, "admin", "Integration-password-123"); err != nil {
		t.Fatal(err)
	}
	server := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	client := testClient{server: server, db: db}
	login := must[store.LoginResult](t, client, "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "Integration-password-123"}, 200)
	client.token = login.AccessToken
	return client
}
func loginAs(t *testing.T, admin testClient, username string) testClient {
	t.Helper()
	c := testClient{server: admin.server, db: admin.db}
	result := must[store.LoginResult](t, c, "POST", "/api/v1/auth/login", map[string]string{"username": username, "password": "Integration-password-123"}, 200)
	c.token = result.AccessToken
	return c
}
func createUser(t *testing.T, c testClient, username, role string) store.User {
	t.Helper()
	return must[store.User](t, c, "POST", "/api/v1/users", map[string]string{"name": username, "username": username, "password": "Integration-password-123", "role": role}, 201)
}

type fixture struct {
	client   testClient
	variant  store.Variant
	location store.Location
	driver   store.User
	customer store.Customer
}

func setupFixture(t *testing.T, c testClient, prefix ...string) fixture {
	driverName := "driver"
	if len(prefix) > 0 {
		driverName = prefix[0] + "driver"
	}
	t.Helper()
	product := must[store.Product](t, c, "POST", "/api/v1/products", store.ProductInput{Name: "ICE"}, 201)
	v := must[store.Variant](t, c, "POST", "/api/v1/variants", store.VariantInput{ProductID: product.ProductID, Name: "5kg", Price: 120}, 201)
	w := must[store.Warehouse](t, c, "POST", "/api/v1/warehouses", store.WarehouseInput{Name: "Kahakootam"}, 201)
	l := must[store.Location](t, c, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: w.WarehouseID, Name: "Main Freezer", Type: "freezer"}, 201)
	driver := createUser(t, c, driverName, store.DeliveryPartner)
	customer := must[store.Customer](t, c, "POST", "/api/v1/customers", store.CustomerInput{Name: "Sunrise Stores"}, 201)
	return fixture{c, v, l, driver, customer}
}
func (f fixture) trip(t *testing.T, customerID string, quantity int64) (store.Trip, store.Order) {
	t.Helper()
	c := f.client
	o := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: customerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: quantity}}}, 201)
	v := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Van"}, 201)
	trip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: v.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{o.OrderID}}, 201)
	return trip, o
}
func (f fixture) deliver(t *testing.T, customerID string, quantity int64, collected float64) (store.CloseStopResult, store.Trip) {
	t.Helper()
	trip, o := f.trip(t, customerID, quantity)
	c := f.client
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/pickup", store.PickupInput{OrderID: o.OrderID, LocationID: f.location.LocationID, Items: o.Items}, 200)
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/start", nil, 200)
	closed := must[store.CloseStopResult](t, c, "POST", "/api/v1/trips/"+trip.TripID+"/stops/"+o.OrderID+"/close", store.CloseStopInput{Delivered: o.Items, CollectedAmount: collected, PaymentMethod: "cash"}, 200)
	return closed, trip
}
func stockAt(t *testing.T, c testClient, locationID, variantID string) int64 {
	t.Helper()
	rows := must[store.List[store.Stock]](t, c, "GET", "/api/v1/stocks?locationId="+locationID+"&variantId="+variantID, nil, 200)
	if len(rows.Items) == 0 {
		return 0
	}
	return rows.Items[0].Quantity
}

func TestMongoAcceptanceAndAtomicStop(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	v2 := must[store.Variant](t, c, "POST", "/api/v1/variants", store.VariantInput{ProductID: f.variant.ProductID, Name: "2.5kg", Price: 65}, 201)
	secondary := must[store.Location](t, c, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: f.location.WarehouseID, Name: "Storeroom Freezer", Type: "freezer"}, 201)
	must[store.Location](t, c, "POST", "/api/v1/locations", store.LocationInput{WarehouseID: f.location.WarehouseID, Name: "Storeroom A", Type: "storeroom"}, 201)
	for _, in := range []store.ReceiveInput{{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 100}, {LocationID: secondary.LocationID, VariantID: f.variant.VariantID, Quantity: 25}, {LocationID: f.location.LocationID, VariantID: v2.VariantID, Quantity: 4}} {
		must[store.Movement](t, c, "POST", "/api/v1/stock/receive", in, 201)
	}
	staff := createUser(t, c, "staff", store.WarehouseStaff)
	must[store.User](t, c, "PUT", "/api/v1/users/"+staff.UserID+"/locations", map[string]any{"locationIds": []string{f.location.LocationID}}, 200)
	sc := loginAs(t, c, "staff")
	dc := loginAs(t, c, "driver")
	createUser(t, c, "otherdriver", store.DeliveryPartner)
	other := loginAs(t, c, "otherdriver")
	wantError(t, sc, "POST", "/api/v1/customers", store.CustomerInput{Name: "Forbidden"}, 403, "FORBIDDEN")
	o := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 20}, {VariantID: v2.VariantID, Quantity: 5}}}, 201)
	van := must[store.Vehicle](t, c, "POST", "/api/v1/vehicles", store.VehicleInput{Name: "Delivery Van"}, 201)
	trip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: van.VehicleID, DeliveryPartnerID: f.driver.UserID, OrderIDs: []string{o.OrderID}}, 201)
	path := "/api/v1/trips/" + trip.TripID
	must[store.Trip](t, c, "PUT", path+"/staff", map[string]any{"warehouseStaffIds": []string{staff.UserID}}, 200)
	wantError(t, sc, "POST", path+"/pickup", store.PickupInput{OrderID: o.OrderID, LocationID: secondary.LocationID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}, 409, "LOCATION_NOT_ASSIGNED")
	wantError(t, dc, "POST", path+"/start", nil, 409, "ORDER_NOT_FULLY_PICKED")
	// A later item fails after the first debit. Both the stock debit AND its
	// movement must be rolled back before the request can be retried.
	pickup := store.PickupInput{OrderID: o.OrderID, LocationID: f.location.LocationID, Items: o.Items}
	wantError(t, sc, "POST", path+"/pickup", pickup, 409, "INSUFFICIENT_STOCK")
	if stockAt(t, c, f.location.LocationID, f.variant.VariantID) != 100 || stockAt(t, c, van.VehicleID, f.variant.VariantID) != 0 {
		t.Fatal("failed pickup leaked stock mutation")
	}
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: v2.VariantID, Quantity: 1}, 201)
	must[store.PickupResult](t, sc, "POST", path+"/pickup", pickup, 200)
	wantError(t, other, "GET", path, nil, 403, "FORBIDDEN")
	otherTrips := must[store.List[store.Trip]](t, other, "GET", "/api/v1/trips", nil, 200)
	if otherTrips.Total != 0 {
		t.Fatal("foreign trip leaked")
	}
	must[store.Trip](t, dc, "POST", path+"/start", nil, 200)
	wantError(t, dc, "POST", path+"/stops/"+o.OrderID+"/close", store.CloseStopInput{Delivered: []store.Item{{VariantID: f.variant.VariantID, Quantity: 21}}}, 400, "VALIDATION_ERROR")
	delivery := store.CloseStopInput{Delivered: []store.Item{{VariantID: f.variant.VariantID, Quantity: 18}, {VariantID: v2.VariantID, Quantity: 5}}, Reason: "Two bags not accepted", CollectedAmount: 3500, PaymentMethod: "cash"}
	type response struct {
		code int
		data []byte
	}
	ch := make(chan response, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, data := dc.call("POST", path+"/stops/"+o.OrderID+"/close", delivery)
			ch <- response{code, data}
		}()
	}
	wg.Wait()
	close(ch)
	var closed store.CloseStopResult
	successes, conflicts := 0, 0
	for r := range ch {
		if r.code == 200 {
			successes++
			if err := json.Unmarshal(r.data, &closed); err != nil {
				t.Fatal(err)
			}
		} else if r.code == 409 && bytes.Contains(r.data, []byte("STOP_ALREADY_CLOSED")) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent result %d %s", r.code, r.data)
		}
	}
	if successes != 1 || conflicts != 1 || closed.Invoice == nil || closed.Payment == nil {
		t.Fatal("stop was not claimed exactly once")
	}
	inv := closed.Invoice
	if inv.Total != 2485 || inv.Lines[0].UnitPrice != 120 || inv.Lines[1].UnitPrice != 65 || inv.InvoiceNo != "INV/"+store.FinancialYear(time.Now())+"/00001" {
		t.Fatalf("incorrect invoice: %+v", inv)
	}
	if stockAt(t, c, van.VehicleID, f.variant.VariantID) != 2 || stockAt(t, c, van.VehicleID, v2.VariantID) != 0 {
		t.Fatal("incorrect van stock after partial delivery")
	}
	ledger := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	if ledger.InvoiceTotal != 2485 || ledger.PaymentTotal != 3500 || ledger.Outstanding != 0 || ledger.AdvanceCredit != 1015 || ledger.Balance != -1015 {
		t.Fatalf("bad ledger: %+v", ledger)
	}
	wantError(t, c, "POST", "/api/v1/invoices/"+inv.InvoiceID+"/cancel", map[string]string{"reason": "No"}, 409, "INVOICE_HAS_PAYMENTS")
	wantError(t, dc, "POST", path+"/complete", nil, 409, "VAN_NOT_EMPTY")
	must[[]store.Movement](t, dc, "POST", path+"/returns", store.ReturnInput{LocationID: f.location.LocationID, Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 2}}}, 200)
	must[store.Trip](t, dc, "POST", path+"/complete", nil, 200)
	settled := must[store.Settlement](t, c, "POST", path+"/settle", map[string]any{"cashHandedOver": 3450, "note": "50 short"}, 201)
	if settled.CashCollected != 3500 || settled.Difference != -50 || !settled.HasDifference {
		t.Fatalf("bad settlement: %+v", settled)
	}
	wantError(t, c, "POST", path+"/settle", map[string]any{"cashHandedOver": 3500}, 409, "TRIP_ALREADY_SETTLED")
	if stockAt(t, c, f.location.LocationID, f.variant.VariantID) != 82 {
		t.Fatal("incorrect returned warehouse stock")
	}
	wantError(t, c, "POST", "/api/v1/variants/"+f.variant.VariantID+"/archive", nil, 409, "VARIANT_IN_USE")
	// Prices are read at delivery, and advance credit from the first stop is
	// consumed without recording a second customer payment.
	must[store.Variant](t, c, "PATCH", "/api/v1/variants/"+f.variant.VariantID+"/price", map[string]float64{"price": 130}, 200)
	second, _ := f.deliver(t, f.customer.CustomerID, 1, 0)
	if second.Invoice.Total != 130 || !strings.HasSuffix(second.Invoice.InvoiceNo, "/00002") {
		t.Fatal("current pricing or gapless series failed")
	}
	ledger = must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger?limit=1", nil, 200)
	if ledger.AdvanceCredit != 885 || ledger.Balance != -885 || len(ledger.Entries) != 1 || ledger.Total != 3 {
		t.Fatal("advance or ledger pagination failed", ledger)
	}
	wantError(t, dc, "GET", path+"/customers/"+f.customer.CustomerID+"/ledger", nil, 403, "FORBIDDEN")
}

func TestMongoOldestFirstCancellationAndSettlementMethods(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 20}, 201)
	first, t1 := f.deliver(t, f.customer.CustomerID, 1, 0)
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+t1.TripID+"/complete", nil, 200)
	second, t2 := f.deliver(t, f.customer.CustomerID, 2, 150)
	allocations := must[store.List[store.Allocation]](t, c, "GET", "/api/v1/paymentAllocations?customerId="+f.customer.CustomerID, nil, 200)
	paid := map[string]float64{}
	for _, a := range allocations.Items {
		paid[a.InvoiceID] += a.Amount
	}
	if paid[first.Invoice.InvoiceID] != 120 || paid[second.Invoice.InvoiceID] != 30 {
		t.Fatal("oldest invoice was not paid first", paid)
	}
	must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: f.customer.CustomerID, TripID: t2.TripID, Amount: 300, Method: "upi"}, 201)
	ledger := must[store.Ledger](t, c, "GET", "/api/v1/customers/"+f.customer.CustomerID+"/ledger", nil, 200)
	if ledger.Outstanding != 0 || ledger.AdvanceCredit != 90 {
		t.Fatal("surplus not credited", ledger)
	}
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+t2.TripID+"/complete", nil, 200)
	settlement := must[store.Settlement](t, c, "POST", "/api/v1/trips/"+t2.TripID+"/settle", map[string]float64{"cashHandedOver": 150}, 201)
	if settlement.CashCollected != 150 || settlement.HasDifference {
		t.Fatal("noncash payment incorrectly counted as cash")
	}
	newCustomer := must[store.Customer](t, c, "POST", "/api/v1/customers", store.CustomerInput{Name: "Unpaid invoice customer"}, 201)
	unpaid, _ := f.deliver(t, newCustomer.CustomerID, 1, 0)
	cancelled := must[store.Invoice](t, c, "POST", "/api/v1/invoices/"+unpaid.Invoice.InvoiceID+"/cancel", map[string]string{"reason": "Issued in error"}, 200)
	if cancelled.InvoiceNo != unpaid.Invoice.InvoiceNo || cancelled.Status != "cancelled" || cancelled.IsActive {
		t.Fatal("cancelled invoice identity was lost")
	}
	ledger = must[store.Ledger](t, c, "GET", "/api/v1/customers/"+newCustomer.CustomerID+"/ledger", nil, 200)
	if ledger.Balance != 0 {
		t.Fatal("cancelled invoice counted in balance")
	}
	if cancelled.InvoiceNo == first.Invoice.InvoiceNo || cancelled.InvoiceNo == second.Invoice.InvoiceNo {
		t.Fatal("invoice number reused")
	}
	// Archival prevents new orders, but must not strand an existing debt.
	must[map[string]bool](t, c, "POST", "/api/v1/customers/"+newCustomer.CustomerID+"/archive", nil, 200)
	wantError(t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: newCustomer.CustomerID, Source: "phone", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}, 404, "NOT_FOUND")
	must[store.Payment](t, c, "POST", "/api/v1/payments", store.PaymentInput{CustomerID: newCustomer.CustomerID, Amount: 10, Method: "bank"}, 201)
	ledger = must[store.Ledger](t, c, "GET", "/api/v1/customers/"+newCustomer.CustomerID+"/ledger", nil, 200)
	if ledger.AdvanceCredit != 10 || ledger.Customer.IsActive {
		t.Fatal("archived ledger reconciliation failed")
	}
}

func TestMongoConcurrentPickupNeverNegative(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 1}, 201)
	trips := []store.Trip{}
	orders := []store.Order{}
	clients := []testClient{}
	for i := 0; i < 2; i++ {
		username := fmt.Sprintf("staff%d", i)
		user := createUser(t, c, username, store.WarehouseStaff)
		must[store.User](t, c, "PUT", "/api/v1/users/"+user.UserID+"/locations", map[string]any{"locationIds": []string{f.location.LocationID}}, 200)
		clients = append(clients, loginAs(t, c, username))
		trip, order := f.trip(t, f.customer.CustomerID, 1)
		must[store.Trip](t, c, "PUT", "/api/v1/trips/"+trip.TripID+"/staff", map[string]any{"warehouseStaffIds": []string{user.UserID}}, 200)
		trips = append(trips, trip)
		orders = append(orders, order)
	}
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i := range trips {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, _ := clients[i].call("POST", "/api/v1/trips/"+trips[i].TripID+"/pickup", store.PickupInput{OrderID: orders[i].OrderID, LocationID: f.location.LocationID, Items: orders[i].Items})
			results <- code
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for code := range results {
		if code == 200 {
			success++
		}
		if code == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("expected one pickup and one insufficient-stock conflict", success, conflict)
	}
	if stockAt(t, c, f.location.LocationID, f.variant.VariantID) != 0 {
		t.Fatal("stock went negative or was duplicated")
	}
	if stockAt(t, c, trips[0].VehicleID, f.variant.VariantID)+stockAt(t, c, trips[1].VehicleID, f.variant.VariantID) != 1 {
		t.Fatal("stock was not conserved")
	}
}

func TestMongoReusedVehicleDoesNotExposeAnotherTrip(t *testing.T) {
	c := testBackend(t)
	f := setupFixture(t, c)
	must[store.Movement](t, c, "POST", "/api/v1/stock/receive", store.ReceiveInput{LocationID: f.location.LocationID, VariantID: f.variant.VariantID, Quantity: 3}, 201)
	_, oldTrip := f.deliver(t, f.customer.CustomerID, 1, 0)
	must[store.Trip](t, c, "POST", "/api/v1/trips/"+oldTrip.TripID+"/complete", nil, 200)
	otherUser := createUser(t, c, "otherpartner", store.DeliveryPartner)
	order := must[store.Order](t, c, "POST", "/api/v1/orders", store.OrderInput{CustomerID: f.customer.CustomerID, Source: "counter", Items: []store.Item{{VariantID: f.variant.VariantID, Quantity: 1}}}, 201)
	newTrip := must[store.Trip](t, c, "POST", "/api/v1/trips", store.TripInput{VehicleID: oldTrip.VehicleID, DeliveryPartnerID: otherUser.UserID, OrderIDs: []string{order.OrderID}}, 201)
	must[store.PickupResult](t, c, "POST", "/api/v1/trips/"+newTrip.TripID+"/pickup", store.PickupInput{OrderID: order.OrderID, LocationID: f.location.LocationID, Items: order.Items}, 200)
	oldPartner := loginAs(t, c, "driver")
	stock := must[store.List[store.Stock]](t, oldPartner, "GET", "/api/v1/trips/"+oldTrip.TripID+"/stock", nil, 200)
	if stock.Total != 0 || len(stock.Items) != 0 {
		t.Fatal("old partner can see another trip's van stock")
	}
	wantError(t, oldPartner, "GET", "/api/v1/trips/"+newTrip.TripID, nil, 403, "FORBIDDEN")
	newPartner := loginAs(t, c, "otherpartner")
	stock = must[store.List[store.Stock]](t, newPartner, "GET", "/api/v1/trips/"+newTrip.TripID+"/stock", nil, 200)
	if stock.Total != 1 || stock.Items[0].Quantity != 1 {
		t.Fatal("own current stock is not visible")
	}
}
