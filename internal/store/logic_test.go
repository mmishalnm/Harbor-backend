package store

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestFinancialYearISTBoundary(t *testing.T) {
	for _, tc := range []struct{ at, year string }{{"2026-03-31T18:29:59Z", "25-26"}, {"2026-03-31T18:30:00Z", "26-27"}, {"2027-03-31T18:29:59Z", "26-27"}, {"2027-03-31T18:30:00Z", "27-28"}} {
		at, err := time.Parse(time.RFC3339, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		if got := FinancialYear(at); got != tc.year {
			t.Fatalf("%s got %s", tc.at, got)
		}
		no, err := InvoiceNumber(at, 99999)
		if err != nil || len(no) > 16 || no != "INV/"+tc.year+"/99999" {
			t.Fatalf("%s %v", no, err)
		}
	}
	for _, n := range []int64{0, -1, 100000} {
		if _, err := InvoiceNumber(time.Now(), n); err == nil {
			t.Fatal("accepted exhausted or invalid series")
		}
	}
}
func TestOldestFirstAndSurplus(t *testing.T) {
	bills := []AllocationBill{{"old", 1000}, {"new", 2485}}
	parts, remaining := AllocateOldest(3500, bills)
	if len(parts) != 2 || parts[0].InvoiceID != "old" || parts[0].Amount != 1000 || parts[1].Amount != 2485 || remaining != 15 {
		t.Fatalf("%+v remainder %v", parts, remaining)
	}
	parts, remaining = AllocateOldest(3500, []AllocationBill{{"new", 2485}})
	if len(parts) != 1 || remaining != 1015 {
		t.Fatal(parts, remaining)
	}
	parts, remaining = AllocateOldest(0.3, []AllocationBill{{"one", 0.1}, {"two", 0.2}})
	if len(parts) != 2 || remaining != 0 || cents(parts[0].Amount)+cents(parts[1].Amount) != 30 {
		t.Fatal(parts, remaining)
	}
	parts, remaining = AllocateOldest(50, []AllocationBill{{"paid", 0}, {"bill", 100}})
	if len(parts) != 1 || parts[0].Amount != 50 || remaining != 0 {
		t.Fatal(parts, remaining)
	}
}
func TestValidationAndDelivery(t *testing.T) {
	for _, items := range [][]Item{nil, {{"a", 0}}, {{"a", -1}}, {{"a", 1}, {"a", 2}}, {{"", 1}}, {{"a", 1000001}}} {
		if validItems(items, false) == nil {
			t.Fatal("accepted invalid items", items)
		}
	}
	ordered := []Item{{"a", 20}, {"b", 5}}
	for _, tc := range []struct {
		delivered []Item
		status    string
	}{{[]Item{}, "failed"}, {[]Item{{"a", 18}, {"b", 5}}, "partiallyDelivered"}, {[]Item{{"b", 5}, {"a", 20}}, "delivered"}} {
		got, err := deliveryStatus(ordered, tc.delivered)
		if err != nil || got != tc.status {
			t.Fatal(got, err)
		}
	}
	if _, err := deliveryStatus(ordered, []Item{{"a", 21}}); err == nil {
		t.Fatal("overdelivery accepted")
	}
	if checkPickup(Actor{Role: WarehouseStaff, LocationIDs: []string{"assigned"}}, "elsewhere") == nil {
		t.Fatal("assignment bypass")
	}
	for _, role := range []string{Admin, Manager} {
		if checkPickup(Actor{Role: role}, "any") != nil {
			t.Fatal("manager override failed")
		}
	}
	for _, p := range []Page{{0, 20}, {1, 201}, {1, 0}, {1000001, 20}} {
		if ValidatePage(p) == nil {
			t.Fatal("invalid pagination accepted")
		}
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), -1, 1e10} {
		if validMoney(v, true) == nil {
			t.Fatal("invalid money accepted")
		}
	}
	if validMoney(0, true) != nil || validMoney(0, false) == nil || RoundMoney(120.126) != 120.13 {
		t.Fatal("money rounding failed")
	}
}
func TestJWTValidation(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	s := &Store{secret: []byte(strings.Repeat("s", 32)), now: func() time.Time { return now }}
	token, err := s.token(User{UserID: "USR-000001", Role: Admin, AccountID: "ACC-000001"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.verify(token)
	if err != nil || c.Subject != "USR-000001" || c.Role != Admin {
		t.Fatal(c, err)
	}
	legacyToken, err := s.token(User{UserID: "USR-000001", Role: Admin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.verify(legacyToken); err == nil {
		t.Fatal("signed token without business ownership was accepted")
	}
	parts := strings.Split(token, ".")
	parts[1] = "e30"
	if _, err := s.verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered token accepted")
	}
	s.now = func() time.Time { return now.Add(8 * time.Hour) }
	if _, err := s.verify(token); err == nil {
		t.Fatal("expired token accepted")
	}
	for _, bad := range []string{"", "abc", "eyJhbGciOiJub25lIn0.e30.", strings.Repeat("a", 4097)} {
		if _, err := s.verify(bad); err == nil {
			t.Fatal("malformed JWT accepted")
		}
	}
}

func TestPickupLocationPermission(t *testing.T) {
	for _, role := range []string{WarehouseStaff, DeliveryPartner} {
		for _, ids := range [][]string{nil, {}, {"LOC-000001"}} {
			actor := Actor{Role: role, LocationIDs: ids}
			if err := checkPickup(actor, "LOC-000001"); err != nil {
				t.Fatalf("allowed pickup rejected: %s %v %v", role, ids, err)
			}
			err := checkPickup(actor, "LOC-000002")
			if len(ids) == 0 && err != nil {
				t.Fatalf("empty assignments should allow all storage: %s %v", role, err)
			}
			if len(ids) > 0 && err == nil {
				t.Fatalf("nonempty assignments bypassed: %s", role)
			}
		}
	}
	for _, role := range []string{Admin, Manager} {
		if err := checkPickup(Actor{Role: role, LocationIDs: []string{"LOC-000001"}}, "LOC-000002"); err != nil {
			t.Fatal("office override rejected", err)
		}
	}
	if err := checkPickup(Actor{Role: "unknown"}, "LOC-000001"); err == nil {
		t.Fatal("unknown role gained unrestricted pickup")
	}
}
