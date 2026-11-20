package store

import "testing"

func TestCustomerPickupValidation(t *testing.T) {
	for _, in := range []OrderInput{{FulfillmentType: "unknown"}, {FulfillmentType: CustomerPickupFulfillment}, {FulfillmentType: CustomerPickupFulfillment, PickupLocationID: "LOC-1", TripID: "TRP-1"}, {PickupLocationID: "LOC-1"}} {
		if validateFulfillment(in) == nil {
			t.Fatal(in)
		}
	}
	for _, in := range []OrderInput{{}, {FulfillmentType: CustomerPickupFulfillment, PickupLocationID: "LOC-1"}} {
		if err := validateFulfillment(in); err != nil {
			t.Fatal(err)
		}
	}
	o := Order{Items: []Item{{VariantID: "MVN-1", Quantity: 5}}}
	for _, in := range []CustomerCollectionInput{{}, {Items: []Item{{VariantID: "MVN-1", Quantity: 6}}}, {Items: []Item{{VariantID: "MVN-2", Quantity: 1}}}, {Items: []Item{{VariantID: "MVN-1", Quantity: 2}}}, {Items: o.Items, CollectedAmount: -1}, {Items: o.Items, CollectedAmount: 1, PaymentMethod: "card"}} {
		if _, err := collectionStatus(o, in); err == nil {
			t.Fatal(in)
		}
	}
	if got, err := collectionStatus(o, CustomerCollectionInput{Items: o.Items}); err != nil || got != "collected" {
		t.Fatal(got, err)
	}
	if got, err := collectionStatus(o, CustomerCollectionInput{Items: []Item{{VariantID: "MVN-1", Quantity: 2}}, Reason: "Only two collected"}); err != nil || got != "partiallyCollected" {
		t.Fatal(got, err)
	}
}
