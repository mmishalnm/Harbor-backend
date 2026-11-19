package store

import (
	"strings"
	"testing"
)

func TestDisplayNameInputValidation(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {"MPN-000001", "MVN-000001", "CUS-000001", "INV-000001"}} {
		if err := validateDisplayNames(DisplayNamesInput{IDs: ids}); err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]string{{"CUS-1/../../users"}, {"507f1f77bcf86cd799439011"}, {"ACC-000001"}, {"CUS-"}, {"CUS-" + strings.Repeat("1", 65)}, make([]string, 201)} {
		if err := validateDisplayNames(DisplayNamesInput{IDs: ids}); err == nil {
			t.Fatalf("accepted invalid display references: %v", ids)
		}
	}
}
