package store

import (
	"harbor/internal/apperr"
	"testing"
)

func TestOrderAssignmentValidation(t *testing.T) {
	for _, tc := range []struct{ orderID, partnerID string }{{"", "USR-000002"}, {" ", "USR-000002"}, {"ORD-000001", ""}, {"ORD-000001", " "}} {
		if err := validateOrderAssignment(tc.orderID, AssignOrderInput{DeliveryPartnerID: tc.partnerID}); err == nil || apperr.Public(err).Code != "VALIDATION_ERROR" {
			t.Fatal("missing assignment field accepted", tc, err)
		}
	}
	if err := validateOrderAssignment("ORD-000001", AssignOrderInput{DeliveryPartnerID: "USR-000002"}); err != nil {
		t.Fatal(err)
	}
}
