package store

import (
	"testing"
	"time"
)

func TestTripSettingsAndIndianDayWindow(t *testing.T) {
	defaults := effectiveOrderSettings(OrderSettings{})
	if defaults.TripDurationDays != 1 || defaults.AutoAssignDeliveryPartnerOrders {
		t.Fatal("wrong defaults", defaults)
	}
	on := true
	for _, days := range []int{0, -1, 366} {
		_, err := validateOrderSettings(OrderSettingsInput{&on, &days})
		if err == nil {
			t.Fatal("invalid trip duration accepted", days)
		}
	}
	if _, err := validateOrderSettings(OrderSettingsInput{}); err == nil {
		t.Fatal("missing settings accepted")
	}
	now := time.Date(2026, 11, 19, 18, 30, 0, 0, time.UTC)
	schedule, err := newTripSchedule(now, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.TripDate != "2026-11-20" || !schedule.StartsAt.Equal(now) || schedule.EndsAt.Sub(schedule.StartsAt) != 24*time.Hour {
		t.Fatal("wrong Indian day", schedule)
	}
	long, err := newTripSchedule(now, "2026-11-19", 3)
	if err != nil || long.EndsAt.Sub(long.StartsAt) != 72*time.Hour {
		t.Fatal("wrong multi-day window", long, err)
	}
	if _, err := newTripSchedule(now, "2026-02-30", 1); err == nil {
		t.Fatal("invalid date accepted")
	}
	trip := Trip{Base: Base{IsActive: true, CreatedAt: now}, TripID: "TRP-000001", DeliveryPartnerID: "USR-000002", Status: "planned", TripSchedule: schedule}
	if !eligibleOrderTrip(trip, "USR-000002", now) || eligibleOrderTrip(trip, "USR-000002", schedule.EndsAt) || eligibleOrderTrip(trip, "someone-else", now) {
		t.Fatal("trip eligibility ignored the time or partner")
	}
	started := trip
	started.TripID = "TRP-000002"
	started.Status = "started"
	if !preferOrderTrip(started, trip) || preferOrderTrip(trip, started) {
		t.Fatal("ongoing trip was not preferred")
	}
	trip.Status = "completed"
	if eligibleOrderTrip(trip, "USR-000002", now) {
		t.Fatal("closed trip accepted")
	}
	legacy := Trip{Base: Base{CreatedAt: now}}
	normalizeTrip(&legacy)
	if legacy.DurationDays != 1 || legacy.TripDate != "2026-11-20" {
		t.Fatal("legacy defaults incorrect", legacy.TripSchedule)
	}
}
