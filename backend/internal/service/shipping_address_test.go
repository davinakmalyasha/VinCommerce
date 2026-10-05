package service

import (
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/domain"
)

func fullAddress() *domain.Address {
	return &domain.Address{
		Recipient:    "Sari Wijaya",
		Phone:        "08123456789",
		AddressLine1: "Jl. Merdeka No. 12",
		City:         "Bandung",
		PostalCode:   "40115",
	}
}

// ResolveAddress returned an inline address with NO field validation, so a POST with an
// empty address body produced a committed order with no recipient, no street and no
// phone.
//
// `shipping_address` is JSONB, so there is no column CHECK to catch it afterwards, and
// the seller UI renders whatever is there. The order reaches a fulfilment queue with
// nothing in it, and the failure surfaces days later as a parcel that cannot be
// delivered and a buyer nobody can call -- after the money has moved.
func TestAnUndeliverableInlineAddressIsRefused(t *testing.T) {
	base := fullAddress()

	cases := []struct {
		name    string
		blank   string
		wantIn  string
		wantErr string
	}{
		{"no recipient", "Recipient", "recipient", "ADDRESS_INCOMPLETE"},
		{"no phone", "Phone", "phone", "ADDRESS_INCOMPLETE"},
		{"no street", "AddressLine1", "address_line1", "ADDRESS_INCOMPLETE"},
		{"no city", "City", "city", "ADDRESS_INCOMPLETE"},
		{"no postal code", "PostalCode", "postal_code", "ADDRESS_INCOMPLETE"},
	}

	for _, c := range cases {
		addr := *base
		setBlank(&addr, c.blank)
		err := validateDeliverableAddress(&addr)
		if err == nil {
			t.Errorf("%s: accepted an address a courier cannot deliver to", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantIn) {
			t.Errorf("%s: error should name the missing field %q, got: %v",
				c.name, c.wantIn, err)
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: error should carry %s, got: %v", c.name, c.wantErr, err)
		}
	}
}

func TestAWhitespaceOnlyFieldCountsAsMissing(t *testing.T) {
	// A single space is what an HTML form submits for a field the user tabbed past and
	// left alone, so a bare `== ""` check passes it and the parcel goes out blank.
	base := fullAddress()
	base.Recipient = "   "
	err := validateDeliverableAddress(base)
	if err == nil {
		t.Fatal("a whitespace-only recipient was accepted; that is what an untouched " +
			"HTML input actually submits")
	}
	if !strings.Contains(err.Error(), "recipient") {
		t.Errorf("error should name recipient, got: %v", err)
	}
}

func TestACompleteAddressIsAccepted(t *testing.T) {
	if err := validateDeliverableAddress(fullAddress()); err != nil {
		t.Fatalf("a complete address was refused: %v", err)
	}
}

// Country is NOT required, and requiring it would reject every checkout that works
// today -- the frontend does not send it on the inline path. Pinning that so a future
// "let's validate everything" pass does not break the product.
func TestCountryIsNotRequired(t *testing.T) {
	addr := fullAddress() // Country deliberately left empty
	if err := validateDeliverableAddress(addr); err != nil {
		t.Fatalf("an address with no country was refused: %v\n"+
			"Country is absent on the inline checkout path by design; requiring it breaks "+
			"checkout", err)
	}
}

// AddressLine2 and Label are genuinely optional and must stay optional.
func TestOptionalFieldsStayOptional(t *testing.T) {
	addr := fullAddress()
	addr.AddressLine2 = ""
	addr.Label = ""
	addr.Province = ""
	addr.Country = ""
	if err := validateDeliverableAddress(addr); err != nil {
		t.Fatalf("optional fields were required: %v", err)
	}
}

// Every missing field must be reported at once. Naming only the first would make the
// buyer fix one field per submission.
func TestAllMissingFieldsAreNamedTogether(t *testing.T) {
	err := validateDeliverableAddress(&domain.Address{})
	if err == nil {
		t.Fatal("an entirely empty address was accepted")
	}
	for _, want := range []string{"recipient", "phone", "address_line1", "city", "postal_code"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q so the buyer can fix it in one pass, got: %v",
				want, err)
		}
	}
}

// The validation must be wired into the path that actually places an order. A helper
// nothing calls is not a validation.
func TestResolveAddressAppliesTheValidation(t *testing.T) {
	src := readSource(t, "order_service.go")
	i := strings.Index(src, "func (s *OrderService) ResolveAddress(")
	if i < 0 {
		t.Fatal("no ResolveAddress")
	}
	body := src[i:]
	if end := strings.Index(body[1:], "\nfunc "); end > 0 {
		body = body[:end]
	}
	at := strings.Index(body, "validateDeliverableAddress(in.Address)")
	if at < 0 {
		t.Fatal("ResolveAddress does not call validateDeliverableAddress.\n" +
			"A helper nothing calls is not a validation, and the inline path would still " +
			"accept a blank address")
	}
	if at > strings.Index(body, "return in.Address") {
		t.Error("validation runs after the address is returned; it must gate the return")
	}
}

func setBlank(a *domain.Address, field string) {
	switch field {
	case "Recipient":
		a.Recipient = ""
	case "Phone":
		a.Phone = ""
	case "AddressLine1":
		a.AddressLine1 = ""
	case "City":
		a.City = ""
	case "PostalCode":
		a.PostalCode = ""
	}
}
