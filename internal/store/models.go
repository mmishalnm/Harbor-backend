package store

import "time"

const (
	Admin           = "admin"
	Manager         = "manager"
	WarehouseStaff  = "warehouseStaff"
	DeliveryPartner = "deliveryPartner"
)

type Base struct {
	IsActive  bool      `json:"isActive" bson:"isActive"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" bson:"updatedAt"`
	Revision  int64     `json:"-" bson:"revision"`
}
type Product struct {
	Base        `bson:",inline"`
	ProductID   string `json:"productId" bson:"productId"`
	Name        string `json:"name" bson:"name"`
	Description string `json:"description" bson:"description"`
}
type Variant struct {
	Base      `bson:",inline"`
	VariantID string  `json:"variantId" bson:"variantId"`
	ProductID string  `json:"productId" bson:"productId"`
	Name      string  `json:"name" bson:"name"`
	SKU       string  `json:"sku" bson:"sku"`
	Price     float64 `json:"price" bson:"price"`
}
type Warehouse struct {
	Base        `bson:",inline"`
	WarehouseID string `json:"warehouseId" bson:"warehouseId"`
	Name        string `json:"name" bson:"name"`
}
type Location struct {
	Base        `bson:",inline"`
	LocationID  string `json:"locationId" bson:"locationId"`
	WarehouseID string `json:"warehouseId" bson:"warehouseId"`
	Name        string `json:"name" bson:"name"`
	Type        string `json:"type" bson:"type"`
}
type Vehicle struct {
	Base           `bson:",inline"`
	VehicleID      string `json:"vehicleId" bson:"vehicleId"`
	Name           string `json:"name" bson:"name"`
	RegistrationNo string `json:"registrationNo" bson:"registrationNo"`
}
type Customer struct {
	Base       `bson:",inline"`
	CustomerID string `json:"customerId" bson:"customerId"`
	Name       string `json:"name" bson:"name"`
	Phone      string `json:"phone" bson:"phone"`
	Address    string `json:"address" bson:"address"`
}
type User struct {
	AssignedVehicleID string `json:"assignedVehicleId,omitempty" bson:"assignedVehicleId,omitempty"`
	AccountID         string `json:"accountId" bson:"accountId"`
	Email             string `json:"email,omitempty" bson:"email,omitempty"`
	Phone             string `json:"phone,omitempty" bson:"phone,omitempty"`
	Base              `bson:",inline"`
	UserID            string   `json:"userId" bson:"userId"`
	Name              string   `json:"name" bson:"name"`
	Username          string   `json:"username" bson:"username"`
	PasswordHash      string   `json:"-" bson:"passwordHash"`
	Role              string   `json:"role" bson:"role"`
	LocationIDs       []string `json:"locationIds" bson:"locationIds"`
}
type Actor struct {
	AssignedVehicleID string
	AccountID         string
	UserID            string
	Role              string
	LocationIDs       []string
}
type Item struct {
	VariantID string `json:"variantId" bson:"variantId"`
	Quantity  int64  `json:"quantity" bson:"quantity"`
}
type Order struct {
	ReservedItems      []Item     `json:"reservedItems" bson:"-"`
	AwaitingStockItems []Item     `json:"awaitingStockItems" bson:"-"`
	CancelReason       string     `json:"cancelReason,omitempty" bson:"cancelReason,omitempty"`
	CancelledBy        string     `json:"cancelledBy,omitempty" bson:"cancelledBy,omitempty"`
	CancelledAt        *time.Time `json:"cancelledAt,omitempty" bson:"cancelledAt,omitempty"`
	Base               `bson:",inline"`
	OrderID            string `json:"orderId" bson:"orderId"`
	CustomerID         string `json:"customerId" bson:"customerId"`
	Source             string `json:"source" bson:"source"`
	Items              []Item `json:"items" bson:"items"`
	Picked             []Item `json:"picked" bson:"picked"`
	TripID             string `json:"tripId,omitempty" bson:"tripId"`
	Status             string `json:"status" bson:"status"`
	CreatedBy          string `json:"createdBy" bson:"createdBy"`
}
type Stop struct {
	OrderID   string     `json:"orderId" bson:"orderId"`
	Status    string     `json:"status" bson:"status"`
	Delivered []Item     `json:"delivered" bson:"delivered"`
	InvoiceID string     `json:"invoiceId,omitempty" bson:"invoiceId"`
	Reason    string     `json:"reason,omitempty" bson:"reason"`
	ClosedAt  *time.Time `json:"closedAt,omitempty" bson:"closedAt"`
}
type Trip struct {
	Name              string `json:"name" bson:"name"`
	TripSchedule      `bson:",inline"`
	WarehouseStaffIDs []string `json:"warehouseStaffIds" bson:"warehouseStaffIds"`
	Base              `bson:",inline"`
	TripID            string `json:"tripId" bson:"tripId"`
	VehicleID         string `json:"vehicleId" bson:"vehicleId"`
	DeliveryPartnerID string `json:"deliveryPartnerId" bson:"deliveryPartnerId"`
	Status            string `json:"status" bson:"status"`
	Stops             []Stop `json:"stops" bson:"stops"`
	VehicleReserved   bool   `json:"-" bson:"vehicleReserved"`
}
type Stock struct {
	ReservedQuantity  int64  `json:"reservedQuantity" bson:"-"`
	AvailableQuantity int64  `json:"availableQuantity" bson:"-"`
	StockKey          string `json:"stockKey" bson:"stockKey"`
	LocationID        string `json:"locationId" bson:"locationId"`
	VariantID         string `json:"variantId" bson:"variantId"`
	Quantity          int64  `json:"quantity" bson:"quantity"`
}
type Movement struct {
	MovementID     string    `json:"movementId" bson:"movementId"`
	VariantID      string    `json:"variantId" bson:"variantId"`
	Quantity       int64     `json:"quantity" bson:"quantity"`
	FromLocationID string    `json:"fromLocationId,omitempty" bson:"fromLocationId"`
	ToLocationID   string    `json:"toLocationId,omitempty" bson:"toLocationId"`
	Kind           string    `json:"kind" bson:"kind"`
	ReferenceID    string    `json:"referenceId,omitempty" bson:"referenceId"`
	TripID         string    `json:"tripId,omitempty" bson:"tripId"`
	Reason         string    `json:"reason" bson:"reason"`
	ActorID        string    `json:"actorId" bson:"actorId"`
	CreatedAt      time.Time `json:"createdAt" bson:"createdAt"`
}
type InvoiceLine struct {
	VariantID string  `json:"variantId" bson:"variantId"`
	Name      string  `json:"name" bson:"name"`
	Quantity  int64   `json:"quantity" bson:"quantity"`
	UnitPrice float64 `json:"unitPrice" bson:"unitPrice"`
	Amount    float64 `json:"amount" bson:"amount"`
}
type Invoice struct {
	Base         `bson:",inline"`
	InvoiceID    string        `json:"invoiceId" bson:"invoiceId"`
	InvoiceNo    string        `json:"invoiceNo" bson:"invoiceNo"`
	CustomerID   string        `json:"customerId" bson:"customerId"`
	OrderID      string        `json:"orderId" bson:"orderId"`
	TripID       string        `json:"tripId" bson:"tripId"`
	Lines        []InvoiceLine `json:"lines" bson:"lines"`
	Total        float64       `json:"total" bson:"total"`
	Status       string        `json:"status" bson:"status"`
	CancelReason string        `json:"cancelReason,omitempty" bson:"cancelReason"`
	CreatedBy    string        `json:"createdBy" bson:"createdBy"`
}
type Payment struct {
	PaymentID  string    `json:"paymentId" bson:"paymentId"`
	CustomerID string    `json:"customerId" bson:"customerId"`
	TripID     string    `json:"tripId,omitempty" bson:"tripId"`
	Amount     float64   `json:"amount" bson:"amount"`
	Method     string    `json:"method" bson:"method"`
	ReceivedBy string    `json:"receivedBy" bson:"receivedBy"`
	CreatedAt  time.Time `json:"createdAt" bson:"createdAt"`
}
type Allocation struct {
	AllocationKey string    `json:"allocationKey" bson:"allocationKey"`
	CustomerID    string    `json:"customerId" bson:"customerId"`
	PaymentID     string    `json:"paymentId" bson:"paymentId"`
	InvoiceID     string    `json:"invoiceId" bson:"invoiceId"`
	Amount        float64   `json:"amount" bson:"amount"`
	CreatedAt     time.Time `json:"createdAt" bson:"createdAt"`
}
type Settlement struct {
	PriorCashHandedOver float64   `json:"priorCashHandedOver" bson:"priorCashHandedOver"`
	SalaryAdjusted      float64   `json:"salaryAdjusted" bson:"salaryAdjusted"`
	TripID              string    `json:"tripId" bson:"tripId"`
	CashCollected       float64   `json:"cashCollected" bson:"cashCollected"`
	CashHandedOver      float64   `json:"cashHandedOver" bson:"cashHandedOver"`
	Difference          float64   `json:"difference" bson:"difference"`
	HasDifference       bool      `json:"hasDifference" bson:"hasDifference"`
	Note                string    `json:"note" bson:"note"`
	SettledBy           string    `json:"settledBy" bson:"settledBy"`
	CreatedAt           time.Time `json:"createdAt" bson:"createdAt"`
}
type Page struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
}
type List[T any] struct {
	Items []T   `json:"items"`
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}
