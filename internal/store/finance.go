package store

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

func validMethod(method string) bool { return method == "cash" || method == "upi" || method == "bank" }
func (s *Store) createInvoice(sc mongo.SessionContext, a Actor, t Trip, o Order, items []Item) (Invoice, error) {
	var out Invoice
	lines := []InvoiceLine{}
	total := int64(0)
	for _, it := range items {
		var v Variant
		if err := s.lock(sc, "variants", "variantId", it.VariantID, &v); err != nil {
			return out, err
		}
		amount := cents(v.Price) * it.Quantity
		total += amount
		if total > 100000000000 {
			return out, apperr.Validation("Invoice total must be at most 1,000,000,000")
		}
		lines = append(lines, InvoiceLine{v.VariantID, v.Name, it.Quantity, v.Price, money(amount)})
	}
	id, err := s.next(sc, "invoice", "INV-")
	if err != nil {
		return out, err
	}
	now := s.now().UTC()
	seq, err := s.sequence(sc, "invoiceNo:"+FinancialYear(now))
	if err != nil {
		return out, err
	}
	no, err := InvoiceNumber(now, seq)
	if err != nil {
		return out, err
	}
	out = Invoice{Base: s.base(), InvoiceID: id, InvoiceNo: no, CustomerID: o.CustomerID, OrderID: o.OrderID, TripID: t.TripID, Lines: lines, Total: money(total), Status: "issued", CreatedBy: a.UserID}
	return out, insert(sc, s.col("invoices"), out)
}
func readAll[T any](ctx context.Context, c *database.Collection, filter bson.M, sortBy bson.D) ([]T, error) {
	out := []T{}
	cur, err := c.Find(ctx, filter, options.Find().SetSort(sortBy))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	err = cur.All(ctx, &out)
	return out, err
}

type ledgerState struct {
	Invoices []Invoice
	Payments []Payment
	Paid     map[string]int64
	Used     map[string]int64
}

func (s *Store) ledgerState(sc mongo.SessionContext, customerID string) (ledgerState, error) {
	out := ledgerState{Paid: map[string]int64{}, Used: map[string]int64{}}
	f := bson.M{"customerId": customerID}
	var err error
	out.Invoices, err = readAll[Invoice](sc, s.col("invoices"), f, bson.D{{Key: "createdAt", Value: 1}, {Key: "invoiceNo", Value: 1}})
	if err != nil {
		return out, err
	}
	out.Payments, err = readAll[Payment](sc, s.col("payments"), f, bson.D{{Key: "createdAt", Value: 1}, {Key: "paymentId", Value: 1}})
	if err != nil {
		return out, err
	}
	cur, err := s.col("paymentAllocations").Find(sc, f)
	if err != nil {
		return out, err
	}
	defer cur.Close(sc)
	for cur.Next(sc) {
		var v Allocation
		if err := cur.Decode(&v); err != nil {
			return out, err
		}
		out.Paid[v.InvoiceID] += cents(v.Amount)
		out.Used[v.PaymentID] += cents(v.Amount)
	}
	return out, cur.Err()
}

// Caller holds the customer revision lock, serializing ALL invoice/payment/
// cancellation operations for this customer without storing an editable balance.
func (s *Store) allocateCredit(sc mongo.SessionContext, customerID string) error {
	state, err := s.ledgerState(sc, customerID)
	if err != nil {
		return err
	}
	bills := []AllocationBill{}
	for _, inv := range state.Invoices {
		if inv.Status == "issued" {
			due := cents(inv.Total) - state.Paid[inv.InvoiceID]
			if due > 0 {
				bills = append(bills, AllocationBill{inv.InvoiceID, money(due)})
			}
		}
	}
	for _, p := range state.Payments {
		credit := cents(p.Amount) - state.Used[p.PaymentID]
		if credit <= 0 {
			continue
		}
		parts, _ := AllocateOldest(money(credit), bills)
		for _, part := range parts {
			v := Allocation{p.PaymentID + ":" + part.InvoiceID, customerID, p.PaymentID, part.InvoiceID, part.Amount, s.now().UTC()}
			if err := insert(sc, s.col("paymentAllocations"), v); err != nil {
				return err
			}
			for i := range bills {
				if bills[i].InvoiceID == part.InvoiceID {
					bills[i].Due = money(cents(bills[i].Due) - cents(part.Amount))
				}
			}
		}
	}
	return nil
}
func (s *Store) recordPayment(sc mongo.SessionContext, a Actor, customerID, tripID string, amount float64, method string) (Payment, error) {
	var out Payment
	if tripID != "" {
		if err := s.lockTripCashPartner(sc, tripID); err != nil {
			return out, err
		}
	}
	if err := validMoney(amount, false); err != nil {
		return out, err
	}
	if !validMethod(method) {
		return out, apperr.Validation("method must be cash, upi, or bank")
	}
	id, err := s.next(sc, "payment", "PAY-")
	if err != nil {
		return out, err
	}
	out = Payment{id, customerID, tripID, RoundMoney(amount), method, a.UserID, s.now().UTC()}
	return out, insert(sc, s.col("payments"), out)
}

type PaymentInput struct {
	CustomerID string  `json:"customerId"`
	TripID     string  `json:"tripId"`
	Amount     float64 `json:"amount"`
	Method     string  `json:"method"`
}

func (s *Store) CollectPayment(ctx context.Context, a Actor, in PaymentInput) (Payment, error) {
	var out Payment
	err := s.tx(ctx, a, []string{Admin, Manager, DeliveryPartner}, func(sc mongo.SessionContext, a Actor) error {
		if in.TripID != "" {
			var t Trip
			if err := s.lock(sc, "trips", "tripId", in.TripID, &t); err != nil {
				return err
			}
			if err := s.tripAccess(a, t); err != nil {
				return err
			}
			if t.Status != "started" {
				return apperr.Conflict("TRIP_NOT_STARTED", "Trip collections require a started trip")
			}
			n, err := s.col("orders").CountDocuments(sc, bson.M{"tripId": in.TripID, "customerId": in.CustomerID})
			if err != nil {
				return err
			}
			if n == 0 {
				return apperr.Forbidden()
			}
		}
		var c Customer
		if err := s.lockCustomerLedger(sc, in.CustomerID, &c); err != nil {
			return err
		}
		var err error
		out, err = s.recordPayment(sc, a, in.CustomerID, in.TripID, in.Amount, in.Method)
		if err != nil {
			return err
		}
		return s.allocateCredit(sc, in.CustomerID)
	})
	return out, err
}

type LedgerEntry struct {
	Method      string    `json:"method,omitempty"`
	EntryID     string    `json:"entryId"`
	Type        string    `json:"type"`
	InvoiceNo   string    `json:"invoiceNo,omitempty"`
	Amount      float64   `json:"amount"`
	Allocated   float64   `json:"allocated"`
	Outstanding float64   `json:"outstanding"`
	CreatedAt   time.Time `json:"createdAt"`
}
type Ledger struct {
	BusinessName  string        `json:"-"`
	GeneratedAt   time.Time     `json:"-"`
	Customer      Customer      `json:"customer"`
	InvoiceTotal  float64       `json:"invoiceTotal"`
	PaymentTotal  float64       `json:"paymentTotal"`
	Outstanding   float64       `json:"outstanding"`
	AdvanceCredit float64       `json:"advanceCredit"`
	Balance       float64       `json:"balance"`
	Entries       []LedgerEntry `json:"entries"`
	Page          int           `json:"page"`
	Limit         int           `json:"limit"`
	Total         int           `json:"total"`
}

func (s *Store) CustomerLedger(ctx context.Context, a Actor, customerID, tripID string, p Page, filterMaps ...map[string]string) (Ledger, error) {
	return s.customerLedger(ctx, a, customerID, tripID, p, false, filterMaps...)
}

func (s *Store) customerLedger(ctx context.Context, a Actor, customerID, tripID string, p Page, all bool, filterMaps ...map[string]string) (Ledger, error) {
	ctx = database.WithAccount(ctx, a.AccountID)
	var out Ledger
	filterMap := map[string]string{}
	if len(filterMaps) > 0 {
		filterMap = filterMaps[0]
	}
	filters, err := parseHistoryFilters(filterMap, false)
	if err != nil {
		return out, err
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	if err := allow(a, Admin, Manager, DeliveryPartner); err != nil {
		return out, err
	}
	err = s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		out = Ledger{Entries: []LedgerEntry{}, Page: p.Page, Limit: p.Limit, GeneratedAt: s.now()}
		if all {
			var account BusinessAccount
			if err := s.find(sc, "accounts", "accountId", a.AccountID, &account); err != nil {
				return err
			}
			out.BusinessName = account.BusinessName
		}
		if a.Role == DeliveryPartner {
			if tripID == "" {
				return apperr.Forbidden()
			}
			var t Trip
			if err := s.find(sc, "trips", "tripId", tripID, &t); err != nil {
				return err
			}
			if err := s.tripAccess(a, t); err != nil {
				return err
			}
			if t.Status != "planned" && t.Status != "started" {
				return apperr.Forbidden()
			}
			n, err := s.col("orders").CountDocuments(sc, bson.M{"tripId": tripID, "customerId": customerID})
			if err != nil {
				return err
			}
			if n == 0 {
				return apperr.Forbidden()
			}
		}
		if err := s.find(sc, "customers", "customerId", customerID, &out.Customer); err != nil {
			return err
		}
		state, err := s.ledgerState(sc, customerID)
		if err != nil {
			return err
		}
		invoices, payments, due, credit := int64(0), int64(0), int64(0), int64(0)
		entries := []LedgerEntry{}
		for _, i := range state.Invoices {
			amount := cents(i.Total)
			kind := "invoice"
			if i.Status == "cancelled" {
				kind = "cancelledInvoice"
				amount = 0
			}
			paid := state.Paid[i.InvoiceID]
			invoices += amount
			due += amount - paid
			entries = append(entries, LedgerEntry{EntryID: i.InvoiceID, Type: kind, InvoiceNo: i.InvoiceNo, Amount: money(amount), Allocated: money(paid), Outstanding: money(amount - paid), CreatedAt: i.CreatedAt})
		}
		for _, v := range state.Payments {
			amount := cents(v.Amount)
			used := state.Used[v.PaymentID]
			payments += amount
			credit += amount - used
			entries = append(entries, LedgerEntry{EntryID: v.PaymentID, Type: "payment", Amount: money(-amount), Allocated: money(used), CreatedAt: v.CreatedAt, Method: v.Method})
		}
		entries = filteredLedgerEntries(entries, filters)
		out.InvoiceTotal = money(invoices)
		out.PaymentTotal = money(payments)
		out.Outstanding = money(due)
		out.AdvanceCredit = money(credit)
		out.Balance = money(invoices - payments)
		out.Total = len(entries)
		if all {
			if len(entries) > 10000 {
				return apperr.Validation("Too many transactions to export; choose a narrower date range")
			}
			out.Entries = entries
			return nil
		}
		start := (p.Page - 1) * p.Limit
		if start < len(entries) {
			end := start + p.Limit
			if end > len(entries) {
				end = len(entries)
			}
			out.Entries = entries[start:end]
		}
		return nil
	})
	return out, err
}
func (s *Store) CancelInvoice(ctx context.Context, a Actor, id, reason string) (Invoice, error) {
	var out Invoice
	if strings.TrimSpace(reason) == "" {
		return out, apperr.Validation("Cancellation reason is required")
	}
	if err := validText(reason); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, a Actor) error {
		if err := s.find(sc, "invoices", "invoiceId", id, &out); err != nil {
			return err
		}
		var c Customer
		if err := s.lockCustomerLedger(sc, out.CustomerID, &c); err != nil {
			return err
		}
		if out.Status != "issued" {
			return apperr.Conflict("INVOICE_ALREADY_CANCELLED", "Invoice has already been cancelled")
		}
		n, err := s.col("paymentAllocations").CountDocuments(sc, bson.M{"invoiceId": id})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("INVOICE_HAS_PAYMENTS", "Only completely unpaid invoices can be cancelled")
		}
		out.Status = "cancelled"
		out.IsActive = false
		out.CancelReason = reason
		return s.update(sc, "invoices", "invoiceId", id, bson.M{"status": "cancelled", "isActive": false, "cancelReason": reason, "cancelledBy": a.UserID})
	})
	return out, err
}
