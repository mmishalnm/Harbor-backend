package store

import "testing"

func TestSignupValidation(t *testing.T) {
	valid := SignupInput{Name: " Arun ", Email: "ARUN@example.com", Phone: "9000000001", Password: "Valid-password-123", BusinessProfileInput: BusinessProfileInput{BusinessName: "Frozen Business", BusinessType: "Frozen foods"}}
	normalized, err := normalizeSignup(valid)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Name != "Arun" || normalized.Username != "arun@example.com" || normalized.Address.Country != "IN" {
		t.Fatal("normalization failed", normalized.Username)
	}
	for _, change := range []func(*SignupInput){func(i *SignupInput) { i.Name = "" }, func(i *SignupInput) { i.BusinessName = "" }, func(i *SignupInput) { i.Email = "Owner <owner@example.com>" }, func(i *SignupInput) { i.Phone = "" }, func(i *SignupInput) { i.Password = "short" }, func(i *SignupInput) { i.Address.Country = "India" }} {
		in := valid
		change(&in)
		if _, err := normalizeSignup(in); err == nil {
			t.Fatal("invalid signup accepted")
		}
	}
}
