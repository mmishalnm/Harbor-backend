package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"harbor/internal/apperr"
	"harbor/internal/database"
)

type CashAllocation struct {
	TripID string  `json:"tripId" bson:"tripId"`
	Amount float64 `json:"amount" bson:"amount"`
}
type PartnerCashEntry struct {
	EntryID       string           `json:"entryId" bson:"entryId"`
	PartnerID     string           `json:"partnerId" bson:"partnerId"`
	Amount        float64          `json:"amount" bson:"amount"`
	Method        string           `json:"method" bson:"method"`
	Note          string           `json:"note" bson:"note"`
	Reference     string           `json:"reference,omitempty" bson:"reference,omitempty"`
	RequestID     string           `json:"requestId" bson:"requestId"`
	SalaryDue     *float64         `json:"salaryDue,omitempty" bson:"salaryDue,omitempty"`
	SalaryPayable *float64         `json:"salaryPayable,omitempty" bson:"salaryPayable,omitempty"`
	Allocations   []CashAllocation `json:"allocations" bson:"allocations"`
	RecordedBy    string           `json:"recordedBy" bson:"recordedBy"`
	CreatedAt     time.Time        `json:"createdAt" bson:"createdAt"`
}
type PartnerCashInput struct {
	Amount    float64  `json:"amount"`
	Method    string   `json:"method"`
	Note      string   `json:"note"`
	Reference string   `json:"reference"`
	RequestID string   `json:"requestId"`
	SalaryDue *float64 `json:"salaryDue"`
}
type PartnerCashSummary struct {
	PartnerID      string  `json:"partnerId"`
	Name           string  `json:"name"`
	IsActive       bool    `json:"isActive"`
	TotalCollected float64 `json:"totalCollected"`
	CashCollected  float64 `json:"cashCollected"`
	UpiCollected   float64 `json:"upiCollected"`
	BankCollected  float64 `json:"bankCollected"`
	CashHandedOver float64 `json:"cashHandedOver"`
	SalaryAdjusted float64 `json:"salaryAdjusted"`
	CashHeld       float64 `json:"cashHeld"`
	OverHanded     float64 `json:"overHanded"`
}
type PartnerCashHistory struct {
	RecordedBy    string    `json:"recordedBy"`
	EntryID       string    `json:"entryId"`
	TripID        string    `json:"tripId,omitempty"`
	Kind          string    `json:"kind"`
	Amount        float64   `json:"amount"`
	Method        string    `json:"method"`
	Note          string    `json:"note"`
	Reference     string    `json:"reference,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	SalaryDue     *float64  `json:"salaryDue,omitempty"`
	SalaryPayable *float64  `json:"salaryPayable,omitempty"`
}
type PartnerCashLedger struct {
	Summary PartnerCashSummary `json:"summary"`
	List[PartnerCashHistory]
}
type PartnerCashResult struct {
	Entry   PartnerCashEntry   `json:"entry"`
	Summary PartnerCashSummary `json:"summary"`
}
type partnerCashState struct {
	Summary   PartnerCashSummary
	History   []PartnerCashHistory
	Trips     []Trip
	Remaining map[string]int64
}

func cashHeld(collected, handed, salary int64) (int64, int64) {
	balance := collected - handed - salary
	return max(balance, 0), max(-balance, 0)
}
func validatePartnerCash(in PartnerCashInput) error {
	if err := validMoney(in.Amount, false); err != nil {
		return err
	}
	if in.Method != "cashHandover" && in.Method != "salaryAdjustment" {
		return apperr.Validation("method must be cashHandover or salaryAdjustment")
	}
	if strings.TrimSpace(in.Note) == "" {
		return apperr.Validation("A settlement note is required")
	}
	if err := validText(in.Note); err != nil {
		return err
	}
	if len(in.Reference) > 200 {
		return apperr.Validation("reference must be at most 200 bytes")
	}
	if len(in.RequestID) < 16 || len(in.RequestID) > 128 {
		return apperr.Validation("requestId must contain 16 to 128 characters for duplicate protection")
	}
	if in.SalaryDue != nil {
		if in.Method != "salaryAdjustment" {
			return apperr.Validation("salaryDue is only supported for a salary adjustment")
		}
		if err := validMoney(*in.SalaryDue, true); err != nil {
			return err
		}
		if cents(*in.SalaryDue) < cents(in.Amount) {
			return apperr.Validation("Salary adjustment cannot exceed salaryDue")
		}
	}
	return nil
}
func sameCashRequest(entry PartnerCashEntry, partnerID string, in PartnerCashInput) bool {
	if entry.PartnerID != partnerID || cents(entry.Amount) != cents(in.Amount) || entry.Method != in.Method || entry.Note != strings.TrimSpace(in.Note) || entry.Reference != strings.TrimSpace(in.Reference) {
		return false
	}
	return (entry.SalaryDue == nil && in.SalaryDue == nil) || (entry.SalaryDue != nil && in.SalaryDue != nil && cents(*entry.SalaryDue) == cents(*in.SalaryDue))
}
func (s *Store) partnerCashState(ctx context.Context, partnerID string) (partnerCashState, error) {
	out := partnerCashState{History: []PartnerCashHistory{}, Remaining: map[string]int64{}}
	var partner User
	if err := s.find(ctx, "users", "userId", partnerID, &partner); err != nil {
		return out, err
	}
	if partner.Role != DeliveryPartner {
		return out, apperr.Validation("partnerId must identify a deliveryPartner")
	}
	out.Summary = PartnerCashSummary{PartnerID: partnerID, Name: partner.Name, IsActive: partner.IsActive}
	var err error
	out.Trips, err = readAll[Trip](ctx, s.col("trips"), bson.M{"deliveryPartnerId": partnerID}, bson.D{{Key: "createdAt", Value: 1}, {Key: "tripId", Value: 1}})
	if err != nil {
		return out, err
	}
	ids := []string{}
	for _, trip := range out.Trips {
		ids = append(ids, trip.TripID)
	}
	payments, err := readAll[Payment](ctx, s.col("payments"), bson.M{"tripId": bson.M{"$in": ids}}, bson.D{})
	if err != nil {
		return out, err
	}
	cash, upi, bank, handed, salary := int64(0), int64(0), int64(0), int64(0), int64(0)
	for _, payment := range payments {
		q := cents(payment.Amount)
		switch payment.Method {
		case "cash":
			cash += q
			out.Remaining[payment.TripID] += q
		case "upi":
			upi += q
		case "bank":
			bank += q
		}
		out.History = append(out.History, PartnerCashHistory{EntryID: payment.PaymentID, TripID: payment.TripID, Kind: "collection", RecordedBy: payment.ReceivedBy, Amount: payment.Amount, Method: payment.Method, CreatedAt: payment.CreatedAt})
	}
	settlements, err := readAll[Settlement](ctx, s.col("settlements"), bson.M{"tripId": bson.M{"$in": ids}}, bson.D{})
	if err != nil {
		return out, err
	}
	for _, settled := range settlements {
		handed += cents(settled.CashHandedOver)
		out.Remaining[settled.TripID] -= cents(settled.CashHandedOver)
		out.History = append(out.History, PartnerCashHistory{EntryID: settled.TripID, TripID: settled.TripID, Kind: "tripHandover", RecordedBy: settled.SettledBy, Amount: -settled.CashHandedOver, Method: "cashHandover", Note: settled.Note, CreatedAt: settled.CreatedAt})
	}
	entries, err := readAll[PartnerCashEntry](ctx, s.col("partnerCashEntries"), bson.M{"partnerId": partnerID}, bson.D{})
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.Method == "cashHandover" {
			handed += cents(entry.Amount)
		} else {
			salary += cents(entry.Amount)
		}
		for _, allocation := range entry.Allocations {
			out.Remaining[allocation.TripID] -= cents(allocation.Amount)
		}
		out.History = append(out.History, PartnerCashHistory{EntryID: entry.EntryID, Kind: entry.Method, RecordedBy: entry.RecordedBy, Amount: -entry.Amount, Method: entry.Method, Note: entry.Note, Reference: entry.Reference, CreatedAt: entry.CreatedAt, SalaryDue: entry.SalaryDue, SalaryPayable: entry.SalaryPayable})
	}
	held, over := cashHeld(cash, handed, salary)
	out.Summary.TotalCollected = money(cash + upi + bank)
	out.Summary.CashCollected = money(cash)
	out.Summary.UpiCollected = money(upi)
	out.Summary.BankCollected = money(bank)
	out.Summary.CashHandedOver = money(handed)
	out.Summary.SalaryAdjusted = money(salary)
	out.Summary.CashHeld = money(held)
	out.Summary.OverHanded = money(over)
	sort.Slice(out.History, func(i, j int) bool {
		if !out.History[i].CreatedAt.Equal(out.History[j].CreatedAt) {
			return out.History[i].CreatedAt.After(out.History[j].CreatedAt)
		}
		return out.History[i].EntryID > out.History[j].EntryID
	})
	return out, nil
}
func (s *Store) PartnerCashList(ctx context.Context, a Actor, p Page) (List[PartnerCashSummary], error) {
	var out List[PartnerCashSummary]
	if err := allow(a, Admin, Manager); err != nil {
		return out, err
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		rows, err := list[User](sc, s, "users", bson.M{"role": DeliveryPartner}, p, bson.D{{Key: "name", Value: 1}, {Key: "userId", Value: 1}})
		if err != nil {
			return err
		}
		out = List[PartnerCashSummary]{Items: []PartnerCashSummary{}, Page: p.Page, Limit: p.Limit, Total: rows.Total}
		for _, partner := range rows.Items {
			state, err := s.partnerCashState(sc, partner.UserID)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, state.Summary)
		}
		return nil
	})
	return out, err
}
func (s *Store) PartnerCash(ctx context.Context, a Actor, partnerID string, p Page) (PartnerCashLedger, error) {
	var out PartnerCashLedger
	if err := allow(a, Admin, Manager, DeliveryPartner); err != nil {
		return out, err
	}
	if a.Role == DeliveryPartner && a.UserID != partnerID {
		return out, apperr.Forbidden()
	}
	if err := ValidatePage(p); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		state, err := s.partnerCashState(sc, partnerID)
		if err != nil {
			return err
		}
		out = PartnerCashLedger{Summary: state.Summary, List: List[PartnerCashHistory]{Items: []PartnerCashHistory{}, Page: p.Page, Limit: p.Limit, Total: int64(len(state.History))}}
		start := (p.Page - 1) * p.Limit
		end := min(start+p.Limit, len(state.History))
		if start < len(state.History) {
			out.Items = state.History[start:end]
		}
		return nil
	})
	return out, err
}
func (s *Store) SettlePartnerCash(ctx context.Context, a Actor, partnerID string, in PartnerCashInput) (PartnerCashResult, error) {
	var out PartnerCashResult
	if err := validatePartnerCash(in); err != nil {
		return out, err
	}
	err := s.tx(ctx, a, []string{Admin, Manager}, func(sc mongo.SessionContext, a Actor) error {
		out = PartnerCashResult{}
		var partner User
		if err := s.lockMatching(sc, "users", "userId", partnerID, false, &partner); err != nil {
			return err
		}
		if partner.Role != DeliveryPartner {
			return apperr.Validation("partnerId must identify a deliveryPartner")
		}
		var existing PartnerCashEntry
		err := s.col("partnerCashEntries").FindOne(sc, bson.M{"requestId": in.RequestID}).Decode(&existing)
		if err == nil {
			if !sameCashRequest(existing, partnerID, in) {
				return apperr.Conflict("IDEMPOTENCY_CONFLICT", "This requestId was already used for different settlement details")
			}
			state, err := s.partnerCashState(sc, partnerID)
			out = PartnerCashResult{existing, state.Summary}
			return err
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		state, err := s.partnerCashState(sc, partnerID)
		if err != nil {
			return err
		}
		if cents(in.Amount) > cents(state.Summary.CashHeld) {
			return apperr.Conflict("CASH_HOLDING_EXCEEDED", "Settlement amount exceeds the cash currently held by this partner")
		}
		id, err := s.next(sc, "payment", "PAY-")
		if err != nil {
			return err
		}
		entry := PartnerCashEntry{EntryID: id, PartnerID: partnerID, Amount: RoundMoney(in.Amount), Method: in.Method, Note: strings.TrimSpace(in.Note), Reference: strings.TrimSpace(in.Reference), RequestID: in.RequestID, RecordedBy: a.UserID, CreatedAt: s.now().UTC(), Allocations: []CashAllocation{}}
		if in.SalaryDue != nil {
			due := RoundMoney(*in.SalaryDue)
			net := money(cents(due) - cents(entry.Amount))
			entry.SalaryDue = &due
			entry.SalaryPayable = &net
		}
		remaining := cents(entry.Amount)
		for _, trip := range state.Trips {
			q := min(remaining, max(state.Remaining[trip.TripID], 0))
			if q > 0 {
				entry.Allocations = append(entry.Allocations, CashAllocation{trip.TripID, money(q)})
				remaining -= q
			}
			if remaining == 0 {
				break
			}
		}
		if remaining != 0 {
			return apperr.Conflict("CASH_HOLDING_EXCEEDED", "No outstanding trip cash remains for this settlement")
		}
		if err := insert(sc, s.col("partnerCashEntries"), entry); err != nil {
			return err
		}
		state, err = s.partnerCashState(sc, partnerID)
		if err != nil {
			return err
		}
		out = PartnerCashResult{entry, state.Summary}
		return nil
	})
	return out, err
}

// The partner lock is shared by collections, trip handover and cash settlement.
func (s *Store) lockTripCashPartner(sc mongo.SessionContext, tripID string) error {
	var trip Trip
	if err := s.find(sc, "trips", "tripId", tripID, &trip); err != nil {
		return err
	}
	var partner User
	return s.lockMatching(sc, "users", "userId", trip.DeliveryPartnerID, false, &partner)
}
func (s *Store) priorTripCash(sc mongo.SessionContext, tripID string) (float64, float64, error) {
	entries, err := readAll[PartnerCashEntry](sc, s.col("partnerCashEntries"), bson.M{"allocations.tripId": tripID}, bson.D{})
	if err != nil {
		return 0, 0, err
	}
	cash, salary := int64(0), int64(0)
	for _, entry := range entries {
		for _, allocation := range entry.Allocations {
			if allocation.TripID == tripID {
				if entry.Method == "cashHandover" {
					cash += cents(allocation.Amount)
				} else {
					salary += cents(allocation.Amount)
				}
			}
		}
	}
	return money(cash), money(salary), nil
}

type TripCashSummary struct {
	CashCollected       float64 `json:"cashCollected"`
	PriorCashHandedOver float64 `json:"priorCashHandedOver"`
	SalaryAdjusted      float64 `json:"salaryAdjusted"`
	TripCashHandedOver  float64 `json:"tripCashHandedOver"`
	RemainingCash       float64 `json:"remainingCash"`
}

func (s *Store) TripCash(ctx context.Context, a Actor, tripID string) (TripCashSummary, error) {
	var out TripCashSummary
	if err := allow(a, Admin, Manager); err != nil {
		return out, err
	}
	ctx = database.WithAccount(ctx, a.AccountID)
	err := s.db.WithTx(ctx, func(sc mongo.SessionContext) error {
		out = TripCashSummary{}
		var trip Trip
		if err := s.find(sc, "trips", "tripId", tripID, &trip); err != nil {
			return err
		}
		payments, err := readAll[Payment](sc, s.col("payments"), bson.M{"tripId": tripID, "method": "cash"}, bson.D{})
		if err != nil {
			return err
		}
		cash := int64(0)
		for _, p := range payments {
			cash += cents(p.Amount)
		}
		out.CashCollected = money(cash)
		out.PriorCashHandedOver, out.SalaryAdjusted, err = s.priorTripCash(sc, tripID)
		if err != nil {
			return err
		}
		var settled Settlement
		err = s.col("settlements").FindOne(sc, bson.M{"tripId": tripID}).Decode(&settled)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		out.TripCashHandedOver = settled.CashHandedOver
		out.RemainingCash = money(max(cash-cents(out.PriorCashHandedOver)-cents(out.SalaryAdjusted)-cents(out.TripCashHandedOver), 0))
		return nil
	})
	return out, err
}
