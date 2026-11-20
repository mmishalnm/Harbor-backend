#!/usr/bin/env python3
"""Regenerate the checked-in OpenAPI contract (JSON is a YAML 1.2 subset)."""
import copy
import base64
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
schemas, examples, paths = {}, {}, {}
A = ["admin", "manager"]
S = A + ["warehouseStaff"]
D = A + ["deliveryPartner"]
ALL = S + ["deliveryPartner"]
stamp = "2026-11-19T09:00:00Z"


def ref(name):
    return {"$ref": "#/components/schemas/" + name}


def arr(item):
    return {"type": "array", "items": item, "maxItems": 200}


def obj(props, required=None):
    result = {"type": "object", "additionalProperties": False, "properties": props}
    fields = required if required is not None else list(props)
    if fields:
        result["required"] = fields
    return result


string = {"type": "string"}
name = {"type": "string", "minLength": 1, "maxLength": 200}
text = {"type": "string", "maxLength": 2000}
amount = {"type": "number", "format": "double", "minimum": 0, "maximum": 1_000_000_000}
quantity = {"type": "integer", "format": "int64", "minimum": 1, "maximum": 1_000_000}
timestamp = {"type": "string", "format": "date-time"}
base = {"isActive": {"type": "boolean"}, "createdAt": timestamp, "updatedAt": timestamp}
base_example = {"isActive": True, "createdAt": stamp, "updatedAt": stamp}


def schema(key, props, example, required=None, entity=False):
    if entity:
        props = {**base, **props}
        example = {**base_example, **example}
    schemas[key] = obj(props, required)
    schemas[key]["example"] = example
    examples[key] = example


schema("Item", {"variantId": string, "quantity": quantity}, {"variantId": "MVN-000001", "quantity": 20})
items = arr(ref("Item"))
schema("ProductInput", {"name": name, "description": text}, {"name": "ICE", "description": "Packaged ice"}, ["name"])
schema("Product", {"productId": string, "name": name, "description": text}, {"productId": "MPN-000001", **examples["ProductInput"]}, entity=True)
schema("VariantInput", {"productId": string, "name": name, "sku": {"type": "string", "maxLength": 100}, "price": {**amount, "minimum": 0.01}}, {"productId": "MPN-000001", "name": "5kg", "sku": "ICE-5", "price": 120}, ["productId", "name", "price"])
schema("Variant", {"variantId": string, **schemas["VariantInput"]["properties"]}, {"variantId": "MVN-000001", **examples["VariantInput"]}, entity=True)
schema("PriceInput", {"price": {**amount, "minimum": 0.01}}, {"price": 120})
schema("WarehouseInput", {"name": name}, {"name": "Kahakootam"})
schema("Warehouse", {"warehouseId": string, "name": name}, {"warehouseId": "WHS-000001", "name": "Kahakootam"}, entity=True)
schema("LocationInput", {"warehouseId": string, "name": name, "type": {"type": "string", "enum": ["freezer", "coldRoom", "storeroom", "rack", "shelf", "other"]}}, {"warehouseId": "WHS-000001", "name": "Main Freezer", "type": "freezer"})
schema("Location", {"locationId": string, **schemas["LocationInput"]["properties"]}, {"locationId": "LOC-000001", **examples["LocationInput"]}, entity=True)
schema("VehicleInput", {"name": name, "registrationNo": {"type": "string", "maxLength": 100}}, {"name": "Delivery Van 1", "registrationNo": "KL-01-AB-1234"}, ["name"])
schema("Vehicle", {"vehicleId": string, **schemas["VehicleInput"]["properties"]}, {"vehicleId": "VEH-000001", **examples["VehicleInput"]}, entity=True)
schema("CustomerInput", {"name": name, "phone": {"type": "string", "maxLength": 30}, "address": text}, {"name": "Sunrise Stores", "phone": "9000000000", "address": "Kahakootam"}, ["name"])
schema("Customer", {"customerId": string, **schemas["CustomerInput"]["properties"]}, {"customerId": "CUS-000001", **examples["CustomerInput"]}, entity=True)
schema("CustomerOption", {"customerId": string, "name": name}, {"customerId": "CUS-000001", "name": "Sunrise Stores"})
schema("BusinessAddress", {"line1": {**string, "maxLength": 200}, "line2": {**string, "maxLength": 200}, "city": {**string, "maxLength": 200}, "state": {**string, "maxLength": 200}, "postalCode": {**string, "maxLength": 20}, "country": {**string, "minLength": 2, "maxLength": 2}}, {"line1": "Main Road", "line2": "", "city": "Thrissur", "state": "Kerala", "postalCode": "680001", "country": "IN"}, [])
business_props = {"businessName": name, "businessType": {**string, "maxLength": 100}, "businessEmail": {**string, "maxLength": 254}, "businessPhone": {**string, "maxLength": 30}, "address": ref("BusinessAddress")}
business_example = {"businessName": "Arun Frozen Foods", "businessType": "Frozen food distribution", "businessEmail": "arun@example.com", "businessPhone": "9000000001", "address": examples["BusinessAddress"]}
schema("BusinessProfileInput", business_props, business_example, ["businessName"])
schema("OrderSettings", {"autoAssignDeliveryPartnerOrders": {"type": "boolean", "default": False}, "tripDurationDays": {"type": "integer", "minimum": 1, "maximum": 365, "default": 1}}, {"autoAssignDeliveryPartnerOrders": True, "tripDurationDays": 1})
schema("DeliveryVehicleInput", {"vehicleId": {**string, "description": "Default vehicle for automatically created trips. Empty string clears it."}}, {"vehicleId": "VEH-000001"})
schema("BusinessAccount", {"accountId": {**string, "pattern": "^ACC-[0-9]{6,}$"}, "ownerUserId": string, "orderSettings": ref("OrderSettings"), **business_props}, {"accountId": "ACC-000001", "ownerUserId": "USR-000001", "orderSettings": {"autoAssignDeliveryPartnerOrders": False, "tripDurationDays": 1}, **business_example}, entity=True)
schema("SignupInput", {"name": name, "username": {**string, "maxLength": 100, "description": "Optional; defaults to email. Globally unique login identifier."}, "email": {**string, "format": "email", "maxLength": 254}, "phone": {**string, "minLength": 1, "maxLength": 30}, "password": {**string, "format": "password", "minLength": 12, "maxLength": 72}, **business_props}, {"name": "Arun", "username": "arun", "email": "arun@example.com", "phone": "9000000001", "password": "Replace-with-a-secret", **business_example}, ["name", "email", "phone", "password", "businessName"])
schema("UserInput", {"name": name, "username": {"type": "string", "minLength": 1, "maxLength": 100}, "password": {"type": "string", "format": "password", "minLength": 12, "maxLength": 72}, "role": {"type": "string", "enum": ["manager", "warehouseStaff", "deliveryPartner"]}, "email": {**string, "format": "email"}, "phone": {**string, "maxLength": 30}}, {"name": "Freezer Staff", "username": "arun.freezerstaff", "password": "Replace-with-a-secret", "role": "warehouseStaff"}, ["name", "username", "password", "role"])
schema("User", {"assignedVehicleId": string, "accountId": string, "userId": string, "name": name, "username": string, "role": {"type": "string", "enum": ALL}, "locationIds": arr(string), "email": string, "phone": string}, {"accountId": "ACC-000001", "userId": "USR-000002", "name": "Freezer Staff", "username": "arun.freezerstaff", "role": "warehouseStaff", "locationIds": []}, [*base, "accountId", "userId", "name", "username", "role", "locationIds"], entity=True)
schema("AssignmentInput", {"locationIds": {**arr(string), "description": "Empty array allows pickup from any active storage location in this business. A nonempty list restricts pickup to those locations."}}, {"locationIds": ["LOC-000001"]})
schema("LoginInput", {"username": string, "password": {"type": "string", "format": "password"}}, {"username": "admin", "password": "Your-ADMIN_PASSWORD"})
admin = {**examples["User"], "userId": "USR-000001", "name": "Administrator", "username": "admin", "role": "admin"}
schema("LoginResult", {"accessToken": string, "tokenType": string, "expiresIn": {"type": "integer"}, "user": ref("User"), "account": ref("BusinessAccount")}, {"accessToken": "<signed-HS256-JWT>", "tokenType": "Bearer", "expiresIn": 28800, "user": admin, "account": examples["BusinessAccount"]})
schema("SessionProfile", {"user": ref("User"), "account": ref("BusinessAccount")}, {"user": admin, "account": examples["BusinessAccount"]})
schema("TripStaffInput", {"warehouseStaffIds": arr(string)}, {"warehouseStaffIds": ["USR-000002"]})
schema("ReceiveInput", {"locationId": string, "variantId": string, "quantity": quantity, "reason": text}, {"locationId": "LOC-000001", "variantId": "MVN-000001", "quantity": 100, "reason": "Opening stock"}, ["locationId", "variantId", "quantity"])
schema("TransferInput", {"fromLocationId": string, "toLocationId": string, "variantId": string, "quantity": quantity, "reason": text}, {"fromLocationId": "LOC-000001", "toLocationId": "LOC-000002", "variantId": "MVN-000001", "quantity": 5, "reason": "Storage transfer"}, ["fromLocationId", "toLocationId", "variantId", "quantity"])
schema("AdjustmentInput", {"locationId": string, "variantId": string, "quantityDelta": {"type": "integer", "minimum": -1_000_000, "maximum": 1_000_000, "description": "Must be nonzero"}, "reason": text}, {"locationId": "LOC-000001", "variantId": "MVN-000001", "quantityDelta": -1, "reason": "Damaged bag"})
schema("Movement", {"movementId": string, "variantId": string, "quantity": quantity, "fromLocationId": string, "toLocationId": string, "kind": string, "referenceId": string, "tripId": string, "reason": text, "actorId": string, "createdAt": timestamp}, {"movementId": "MOV-000001", "variantId": "MVN-000001", "quantity": 100, "toLocationId": "LOC-000001", "kind": "receive", "reason": "Opening stock", "actorId": "USR-000001", "createdAt": stamp}, ["movementId", "variantId", "quantity", "kind", "reason", "actorId", "createdAt"])
stock_quantity = {"type": "integer", "format": "int64", "minimum": 0}
schema("Stock", {"stockKey": string, "locationId": string, "variantId": string, "quantity": {**stock_quantity, "description": "Physical on-hand quantity; changes only through stock movements."}, "reservedQuantity": {**stock_quantity, "description": "Computed storage reservation, not an editable field."}, "availableQuantity": {**stock_quantity, "description": "Unreserved storage quantity. Trip vehicle stock uses quantity instead."}}, {"stockKey": "LOC-000001:MVN-000001", "locationId": "LOC-000001", "variantId": "MVN-000001", "quantity": 225, "reservedQuantity": 13, "availableQuantity": 212})
availability_props = {"onHandQuantity": stock_quantity, "reservedQuantity": stock_quantity, "availableQuantity": stock_quantity, "awaitingStockQuantity": stock_quantity}
availability_example = {"onHandQuantity": 225, "reservedQuantity": 13, "availableQuantity": 212, "awaitingStockQuantity": 0}
schema("AvailabilityTotals", availability_props, availability_example)
schema("Availability", {"variantId": string, **availability_props}, {"variantId": "MVN-000001", **availability_example})
discount_type = {"type": "string", "enum": ["percentage", "fixed"]}
schema("DiscountInput", {"type": discount_type, "value": {**amount, "minimum": 0.01}, "reason": {**text, "minLength": 1}}, {"type": "fixed", "value": 100, "reason": "Customer loyalty"})
schema("Discount", {"couponId": string, "code": string, "name": name, "type": discount_type, "value": amount, "reason": text}, {"couponId": "CPN-000001", "code": "WELCOME10", "name": "Welcome discount", "type": "percentage", "value": 10}, ["type", "value"])
schema("CouponInput", {"name": name, "code": {**string, "minLength": 2, "maxLength": 32, "description": "Case-insensitive letters, digits, underscores and hyphens. Stored uppercase; unique within your business, including archived codes."}, "type": discount_type, "value": {**amount, "minimum": 0.01, "description": "Percentage: greater than zero, at most 100. Fixed: INR. Rounded to two decimals."}}, {"name": "Welcome discount", "code": "WELCOME10", "type": "percentage", "value": 10})
schema("Coupon", {"couponId": string, **schemas["CouponInput"]["properties"]}, {"couponId": "CPN-000001", **examples["CouponInput"]}, entity=True)
schema("CouponList", {"items": arr(ref("Coupon")), "page": {"type": "integer"}, "limit": {"type": "integer"}, "total": {"type": "integer"}}, {"items": [examples["Coupon"]], "page": 1, "limit": 20, "total": 1})
schema("OrderInput", {"tripId": {**string, "description": "Optional explicit planned/started trip. Riders can choose only their own trip; overrides automatic trip selection."}, "customerId": string, "source": {"type": "string", "enum": ["counter", "salesStaff", "store", "phone", "pickup", "deliveryPartner"]}, "items": {**items, "minItems": 1}}, {"customerId": "CUS-000001", "source": "phone", "items": [{"variantId": "MVN-000001", "quantity": 20}, {"variantId": "MVN-000002", "quantity": 5}]})
schemas["OrderInput"]["properties"].update({"couponId": {**string, "description": "Optional active business coupon. Mutually exclusive with discount."}, "discount": {**ref("DiscountInput"), "description": "Admin/manager only. Manual discount requires a reason. Cannot combine with a coupon."}})
schemas["OrderInput"]["required"] = ["customerId", "items"]
schemas["OrderInput"]["properties"]["source"]["description"] = "Delivery partners may omit source (defaults to deliveryPartner). Other roles must supply it."
schema("Order", {"orderId": string, **schemas["OrderInput"]["properties"], "picked": items, "reservedItems": items, "awaitingStockItems": items, "cancelReason": text, "cancelledBy": string, "cancelledAt": timestamp, "tripId": string, "status": string, "createdBy": string}, {"orderId": "ORD-000001", **examples["OrderInput"], "picked": [], "reservedItems": examples["OrderInput"]["items"], "awaitingStockItems": [], "status": "open", "createdBy": "USR-000001"}, [*base, "orderId", "customerId", "source", "items", "picked", "status", "createdBy"], entity=True)
schemas["Order"]["properties"].pop("couponId", None)
schemas["Order"]["properties"]["discount"] = ref("Discount")
schema("AssignOrderInput", {"tripId": {**string, "description": "Optional explicit trip instead of automatic trip selection."}, "deliveryPartnerId": string, "vehicleId": {**string, "description": "Optional specific vehicle. Otherwise reuse the partner's current trip, or create a trip using their default vehicle."}}, {"deliveryPartnerId": "USR-000003", "vehicleId": "VEH-000001"}, [])
schemas["AssignOrderInput"]["anyOf"] = [{"required": ["tripId"]}, {"required": ["deliveryPartnerId"]}]
schema("Stop", {"orderId": string, "status": {"type": "string", "enum": ["pending", "delivered", "partiallyDelivered", "failed"]}, "delivered": items, "invoiceId": string, "reason": text, "closedAt": timestamp}, {"orderId": "ORD-000001", "status": "pending", "delivered": []}, ["orderId", "status", "delivered"])
schema("TripInput", {"name": {**name, "description": "Optional readable name, e.g. Morning run. Defaults to a distinct Trip number."}, "startDate": {**string, "format": "date", "description": "Optional Indian calendar start date; defaults to today. Duration snapshots the business setting."}, "vehicleId": string, "deliveryPartnerId": string, "orderIds": arr(string), "warehouseStaffIds": arr(string)}, {"vehicleId": "VEH-000001", "deliveryPartnerId": "USR-000003", "orderIds": ["ORD-000001"], "warehouseStaffIds": ["USR-000002"]}, [])
schemas["TripInput"]["description"] = "Admin/manager must supply vehicleId and deliveryPartnerId. Riders may omit them to use their assigned vehicle and own identity; may only plan today, choose visible assigned vehicles, and add their own open orders. Riders cannot assign warehouse staff."
schema("Trip", {"name": name, "tripDate": {**string, "format": "date"}, "durationDays": {"type": "integer", "minimum": 1, "maximum": 365}, "startsAt": timestamp, "endsAt": timestamp, "warehouseStaffIds": arr(string), "tripId": string, "vehicleId": string, "deliveryPartnerId": string, "status": {"type": "string", "enum": ["planned", "started", "completed", "settled"]}, "stops": arr(ref("Stop"))}, {"name": "Morning run", "tripDate": "2026-11-19", "durationDays": 1, "startsAt": "2026-11-18T18:30:00Z", "endsAt": "2026-11-19T18:30:00Z", "warehouseStaffIds": ["USR-000002"], "tripId": "TRP-000001", "vehicleId": "VEH-000001", "deliveryPartnerId": "USR-000003", "status": "planned", "stops": [examples["Stop"]]}, entity=True)
schema("PickupInput", {"orderId": string, "order": ref("OrderInput"), "locationId": string, "items": {**items, "minItems": 1}}, {"orderId": "ORD-000001", "locationId": "LOC-000001", "items": examples["OrderInput"]["items"]}, ["locationId", "items"])
schemas["PickupInput"]["oneOf"] = [{"required": ["orderId"], "not": {"required": ["order"]}}, {"required": ["order"], "not": {"required": ["orderId"]}}]
picked_order = {**examples["Order"], "tripId": "TRP-000001", "picked": examples["OrderInput"]["items"], "status": "assigned"}
pickup_move = {**examples["Movement"], "quantity": 20, "fromLocationId": "LOC-000001", "toLocationId": "VEH-000001", "kind": "pickup", "referenceId": "ORD-000001", "tripId": "TRP-000001", "reason": ""}
schema("PickupResult", {"tripId": string, "order": ref("Order"), "movements": arr(ref("Movement"))}, {"tripId": "TRP-000001", "order": picked_order, "movements": [pickup_move, {**pickup_move, "movementId": "MOV-000002", "variantId": "MVN-000002", "quantity": 5}]})
method = {"type": "string", "enum": ["cash", "upi", "bank"]}
delivered = [{"variantId": "MVN-000001", "quantity": 18}, {"variantId": "MVN-000002", "quantity": 5}]
schema("CloseStopInput", {"delivered": items, "reason": text, "collectedAmount": amount, "paymentMethod": method}, {"delivered": delivered, "reason": "Two bags not accepted", "collectedAmount": 3500, "paymentMethod": "cash"}, ["delivered"])
line_props = {"variantId": string, "name": string, "quantity": quantity, "unitPrice": amount, "subtotal": amount, "discountAmount": amount, "amount": {**amount, "description": "Net line amount after its allocated share of the order discount."}}
schema("InvoiceLine", line_props, {"variantId": "MVN-000001", "name": "5kg", "quantity": 18, "unitPrice": 120, "subtotal": 2160, "discountAmount": 0, "amount": 2160})
invoice = {"invoiceId": "INV-000001", "invoiceNo": "INV/26-27/00001", "customerId": "CUS-000001", "orderId": "ORD-000001", "tripId": "TRP-000001", "lines": [examples["InvoiceLine"], {"variantId": "MVN-000002", "name": "2.5kg", "quantity": 5, "unitPrice": 65, "subtotal": 325, "discountAmount": 0, "amount": 325}], "subtotal": 2485, "discountAmount": 0, "total": 2485, "status": "issued", "createdBy": "USR-000003"}
schema("Invoice", {"invoiceId": string, "invoiceNo": {"type": "string", "maxLength": 16, "pattern": "^INV/[0-9]{2}-[0-9]{2}/[0-9]{5}$"}, "customerId": string, "orderId": string, "tripId": string, "lines": arr(ref("InvoiceLine")), "subtotal": amount, "discountAmount": amount, "discount": ref("Discount"), "total": {**amount, "description": "Net payable amount. Subtotal minus discountAmount; sums net line amounts."}, "status": {"type": "string", "enum": ["issued", "cancelled"]}, "cancelReason": text, "createdBy": string}, invoice, [*base, *invoice], entity=True)
schema("PaymentInput", {"customerId": string, "tripId": string, "amount": {**amount, "minimum": 0.01}, "method": method}, {"customerId": "CUS-000001", "tripId": "TRP-000001", "amount": 3500, "method": "cash"}, ["customerId", "amount", "method"])
payment = {"paymentId": "PAY-000001", "customerId": "CUS-000001", "tripId": "TRP-000001", "amount": 3500, "method": "cash", "receivedBy": "USR-000003", "createdAt": stamp}
schema("Payment", {"paymentId": string, **schemas["PaymentInput"]["properties"], "receivedBy": string, "createdAt": timestamp}, payment, ["paymentId", "customerId", "amount", "method", "receivedBy", "createdAt"])
schema("Allocation", {"allocationKey": string, "customerId": string, "paymentId": string, "invoiceId": string, "amount": amount, "createdAt": timestamp}, {"allocationKey": "PAY-000001:INV-000001", "customerId": "CUS-000001", "paymentId": "PAY-000001", "invoiceId": "INV-000001", "amount": 2485, "createdAt": stamp})
closed_stop = {"orderId": "ORD-000001", "status": "partiallyDelivered", "delivered": delivered, "invoiceId": "INV-000001", "reason": "Two bags not accepted", "closedAt": stamp}
schema("CloseStopResult", {"stop": ref("Stop"), "invoice": ref("Invoice"), "payment": ref("Payment")}, {"stop": closed_stop, "invoice": examples["Invoice"], "payment": payment}, ["stop"])
schema("ReturnInput", {"locationId": string, "items": {**items, "minItems": 1}, "reason": text}, {"locationId": "LOC-000001", "items": [{"variantId": "MVN-000001", "quantity": 2}], "reason": "Undelivered goods"}, ["locationId", "items"])
schema("SettlementInput", {"cashHandedOver": amount, "note": text}, {"cashHandedOver": 3450, "note": "50 short"}, ["cashHandedOver"])
schema("Settlement", {"tripId": string, "cashCollected": amount, "cashHandedOver": amount, "difference": {"type": "number", "format": "double", "description": "Additional cashHandedOver plus priorCashHandedOver plus salaryAdjusted minus cashCollected; negative means shortage"}, "hasDifference": {"type": "boolean"}, "note": text, "settledBy": string, "createdAt": timestamp}, {"tripId": "TRP-000001", "cashCollected": 3500, "cashHandedOver": 3450, "difference": -50, "hasDifference": True, "note": "50 short", "settledBy": "USR-000001", "createdAt": stamp})
schema("LedgerEntry", {"method": {**string, "enum": ["cash", "upi", "bank"]}, "entryId": string, "type": string, "invoiceNo": string, "amount": {"type": "number"}, "allocated": amount, "outstanding": amount, "createdAt": timestamp}, {"entryId": "INV-000001", "type": "invoice", "invoiceNo": "INV/26-27/00001", "amount": 2485, "allocated": 2485, "outstanding": 0, "createdAt": stamp}, ["entryId", "type", "amount", "allocated", "outstanding", "createdAt"])
page_props = {"page": {"type": "integer", "minimum": 1}, "limit": {"type": "integer", "minimum": 1, "maximum": 200}, "total": {"type": "integer", "minimum": 0}}
schema("Ledger", {"customer": ref("Customer"), "invoiceTotal": amount, "paymentTotal": amount, "outstanding": amount, "advanceCredit": amount, "balance": {"type": "number", "description": "invoiceTotal minus paymentTotal; negative means customer credit"}, "entries": arr(ref("LedgerEntry")), **page_props}, {"customer": examples["Customer"], "invoiceTotal": 2485, "paymentTotal": 3500, "outstanding": 0, "advanceCredit": 1015, "balance": -1015, "entries": [examples["LedgerEntry"], {"entryId": "PAY-000001", "type": "payment", "amount": -3500, "allocated": 2485, "outstanding": 0, "createdAt": stamp}], "page": 1, "limit": 20, "total": 2})
examples["Ledger"]["entries"] = [{"entryId": "PAY-000001", "type": "payment", "method": "cash", "amount": -3500, "allocated": 2485, "outstanding": 0, "createdAt": stamp}, examples["LedgerEntry"]]
# A valid miniature PDF keeps Swagger's expected example downloadable.
stream = b"BT /F1 14 Tf 40 780 Td (Harbor - Sunrise Stores ledger) Tj 0 -24 Td (Invoice total INR 2485.00; paid INR 3500.00) Tj 0 -24 Td (Advance credit INR 1015.00) Tj ET"
objects = [b"<< /Type /Catalog /Pages 2 0 R >>", b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>", b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream"]
example_pdf = b"%PDF-1.4\n"
offsets = [0]
for i, pdf_object in enumerate(objects, 1):
    offsets.append(len(example_pdf))
    example_pdf += str(i).encode() + b" 0 obj\n" + pdf_object + b"\nendobj\n"
xref_at = len(example_pdf)
example_pdf += b"xref\n0 6\n0000000000 65535 f \n" + b"".join(f"{offset:010d} 00000 n \n".encode() for offset in offsets[1:])
example_pdf += f"trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n{xref_at}\n%%EOF\n".encode()
schema("LedgerDocument", {"fileName": string, "mimeType": {**string, "enum": ["application/pdf"]}, "data": {**string, "format": "byte", "description": "Base64-encoded PDF. Includes all matching transactions, not just the current page."}}, {"fileName": "Sunrise-Stores-ledger.pdf", "mimeType": "application/pdf", "data": base64.b64encode(example_pdf).decode()})
cash_summary_props = {"partnerId": string, "name": name, "isActive": {"type": "boolean"}, "totalCollected": amount, "cashCollected": amount, "upiCollected": amount, "bankCollected": amount, "cashHandedOver": amount, "salaryAdjusted": amount, "cashHeld": amount, "overHanded": amount}
cash_summary_example = {"partnerId": "USR-000003", "name": "Delivery Partner", "isActive": True, "totalCollected": 4000, "cashCollected": 3500, "upiCollected": 500, "bankCollected": 0, "cashHandedOver": 1000, "salaryAdjusted": 500, "cashHeld": 2000, "overHanded": 0}
schema("PartnerCashSummary", cash_summary_props, cash_summary_example)
schema("CashAllocation", {"tripId": string, "paymentId": {**string, "description": "Receipt ID for an independent collection, instead of tripId."}, "amount": amount}, {"tripId": "TRP-000001", "amount": 500}, ["amount"])
schemas["CashAllocation"]["oneOf"] = [{"required": ["tripId"], "not": {"required": ["paymentId"]}}, {"required": ["paymentId"], "not": {"required": ["tripId"]}}]
schema("PartnerCashInput", {"amount": {**amount, "minimum": 0.01}, "method": {**string, "enum": ["cashHandover", "salaryAdjustment"]}, "note": {**text, "minLength": 1}, "reference": {**string, "maxLength": 200}, "requestId": {**string, "minLength": 16, "maxLength": 128, "description": "Client-generated immutable idempotency key. Reuse only to retry this exact settlement."}, "salaryDue": {**amount, "description": "Optional, salaryAdjustment only. Must cover the adjustment; salaryPayable is computed on the server."}}, {"amount": 500, "method": "salaryAdjustment", "note": "Adjusted against November salary", "reference": "November 2026 salary", "requestId": "cash-settlement-example-0001", "salaryDue": 10000}, ["amount", "method", "note", "requestId"])
schema("PartnerCashEntry", {"entryId": string, "partnerId": string, **schemas["PartnerCashInput"]["properties"], "salaryPayable": amount, "allocations": {"type": "array", "items": ref("CashAllocation")}, "recordedBy": string, "createdAt": timestamp}, {"entryId": "PAY-000003", "partnerId": "USR-000003", **examples["PartnerCashInput"], "salaryPayable": 9500, "allocations": [examples["CashAllocation"]], "recordedBy": "USR-000001", "createdAt": stamp}, ["entryId", "partnerId", "amount", "method", "note", "requestId", "allocations", "recordedBy", "createdAt"])
schema("PartnerCashHistory", {"customerId": string, "recordedBy": string, "entryId": string, "tripId": string, "kind": string, "amount": {"type": "number"}, "method": string, "note": text, "reference": string, "createdAt": timestamp, "salaryDue": amount, "salaryPayable": amount}, {"entryId": "PAY-000003", "kind": "salaryAdjustment", "amount": -500, "method": "salaryAdjustment", "note": "Adjusted against November salary", "reference": "November 2026 salary", "createdAt": stamp, "salaryDue": 10000, "salaryPayable": 9500}, ["entryId", "kind", "amount", "method", "note", "createdAt"])
schema("PartnerCashList", {"items": arr(ref("PartnerCashSummary")), **page_props}, {"items": [cash_summary_example], "page": 1, "limit": 20, "total": 1})
schema("PartnerCashLedger", {"summary": ref("PartnerCashSummary"), "items": arr(ref("PartnerCashHistory")), **page_props}, {"summary": cash_summary_example, "items": [examples["PartnerCashHistory"]], "page": 1, "limit": 20, "total": 1})
schema("PartnerCashResult", {"entry": ref("PartnerCashEntry"), "summary": ref("PartnerCashSummary")}, {"entry": examples["PartnerCashEntry"], "summary": cash_summary_example})
schema("TripCashSummary", {"cashCollected": amount, "priorCashHandedOver": amount, "salaryAdjusted": amount, "tripCashHandedOver": amount, "remainingCash": amount}, {"cashCollected": 3500, "priorCashHandedOver": 1000, "salaryAdjusted": 500, "tripCashHandedOver": 0, "remainingCash": 2000})
schemas["Settlement"]["properties"].update({"priorCashHandedOver": amount, "salaryAdjusted": amount})
examples["Settlement"].update({"priorCashHandedOver": 0, "salaryAdjusted": 0})
schema("ReasonInput", {"reason": text}, {"reason": "Invoice issued in error"})
schema("Archived", {"isActive": {"type": "boolean", "enum": [False]}}, {"isActive": False})
schema("DisplayNamesInput", {"ids": {"type": "array", "maxItems": 200, "items": {"type": "string", "maxLength": 64, "pattern": "^(MPN|MVN|WHS|LOC|VEH|CUS|USR|ORD|TRP|INV|PAY|MOV)-[0-9]+$"}}}, {"ids": ["MVN-000001", "LOC-000001", "CUS-000001", "VEH-000001"]})
schema("DisplayNamesResult", {"names": {"type": "object", "additionalProperties": {"type": "string"}}}, {"names": {"MVN-000001": "ICE · 5kg", "LOC-000001": "Main Freezer · Kahakootam", "CUS-000001": "Sunrise Stores", "VEH-000001": "Delivery Van 1"}})
schema("BulkAssignOrdersInput", {"orderIds": {**arr(string), "minItems": 1, "uniqueItems": True}, **schemas["AssignOrderInput"]["properties"]}, {"orderIds": ["ORD-000001", "ORD-000002"], "tripId": "TRP-000001"}, ["orderIds"])
schemas["BulkAssignOrdersInput"]["anyOf"] = [{"required": ["tripId"]}, {"required": ["deliveryPartnerId"]}]
schema("BulkCancelOrdersInput", {"orderIds": {**arr(string), "minItems": 1, "uniqueItems": True}, "reason": {**text, "minLength": 1}}, {"orderIds": ["ORD-000001", "ORD-000002"], "reason": "Customer cancelled before assignment"})
schema("BulkOrdersResult", {"orders": arr(ref("Order")), "tripId": string}, {"orders": [{**examples["Order"], "tripId": "TRP-000001", "status": "assigned"}], "tripId": "TRP-000001"}, ["orders"])
schema("Health", {"status": string}, {"status": "ok"})
schema("Error", {"error": obj({"code": string, "message": string})}, {"error": {"code": "VALIDATION_ERROR", "message": "Name is required and must be at most 200 bytes"}})


def page_params():
    return [{"in": "query", "name": "page", "schema": {"type": "integer", "minimum": 1, "maximum": 1_000_000, "default": 1}},
            {"in": "query", "name": "limit", "schema": {"type": "integer", "minimum": 1, "maximum": 200, "default": 20}}]


def operation(method, path, summary, roles, response, request=None, status=200, example=None, description="", paginate=False):
    params = [{"in": "path", "name": part[1:-1], "required": True, "schema": string}
              for part in path.split("/") if part.startswith("{")]
    if paginate:
        params += page_params()
    tag = path.split("/")[3] if path.startswith("/api/v1/") else "system"
    op = {"summary": summary, "operationId": method + path.replace("/", "_").replace("{", "").replace("}", ""), "tags": [tag], "x-roles": roles,
          "description": description or ("Allowed roles: " + ", ".join(roles) if roles else "Public endpoint."),
          "security": [{"bearerAuth": []}] if roles else [], "x-account-isolation": bool(roles), "parameters": params,
          "responses": {str(status): {"description": "Expected response", "content": {"application/json": {"schema": ref(response), "example": example if example is not None else examples[response]}}}}}
    if request:
        op["requestBody"] = {"required": True, "content": {"application/json": {"schema": ref(request), "example": examples[request]}}}
    errors = {"400": ("VALIDATION_ERROR", "Request input is invalid"), "401": ("UNAUTHORIZED", "Bearer authentication is required"), "403": ("FORBIDDEN", "You do not have access to this operation"), "404": ("NOT_FOUND", "Resource not found"), "409": ("INSUFFICIENT_STOCK", "Source location has insufficient stock"), "500": ("INTERNAL_ERROR", "An internal error occurred")}
    if path == "/api/v1/auth/signup":
        errors["409"] = ("LOGIN_EXISTS", "This username or email is already registered")
    if path == "/api/v1/users":
        errors["409"] = ("USERNAME_EXISTS", "Username or email already exists")
    if path == "/api/v1/variants/{id}/archive":
        errors["409"] = ("VARIANT_IN_USE", "Clear this variant stock and finish open orders before archiving it")
    if "stops/" in path:
        errors["409"] = ("STOP_ALREADY_CLOSED", "This stop has already been closed")
    if request:
        errors["415"] = ("UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
    for code, (key, message) in errors.items():
        op["responses"][code] = {"description": message, "content": {"application/json": {"schema": ref("Error"), "example": {"error": {"code": key, "message": message}}}}}
    paths.setdefault(path, {})[method.lower()] = op
    return op


sales_money = {"type": "number", "format": "double", "minimum": 0}
schema("SalesPeriod", {"dateFrom": {**string, "format": "date"}, "dateTo": {**string, "format": "date"}, "timeZone": {**string, "enum": ["Asia/Kolkata"]}}, {"dateFrom": "2026-11-20", "dateTo": "2026-11-20", "timeZone": "Asia/Kolkata"})
schema("SalesSummary", {"salesAmount": sales_money, "unitsSold": {"type": "integer", "minimum": 0}, "invoiceCount": {"type": "integer", "minimum": 0}, "customerCount": {"type": "integer", "minimum": 0}, "averageInvoiceValue": sales_money}, {"salesAmount": 2485, "unitsSold": 23, "invoiceCount": 1, "customerCount": 1, "averageInvoiceValue": 2485})
schema("SalesCollections", {key: sales_money for key in ["total", "cash", "upi", "bank"]}, {"total": 3500, "cash": 3500, "upi": 0, "bank": 0})
schemas["SalesCollections"]["nullable"] = True
schema("SalesVariant", {"productId": string, "productName": string, "variantId": string, "variantName": string, "unitsSold": {"type": "integer", "minimum": 0}, "salesAmount": sales_money, "invoiceCount": {"type": "integer", "minimum": 0}, "averageUnitPrice": sales_money}, {"productId": "MPN-000001", "productName": "ICE", "variantId": "MVN-000001", "variantName": "5kg", "unitsSold": 18, "salesAmount": 2160, "invoiceCount": 1, "averageUnitPrice": 120})
schema("SalesPoint", {"date": {**string, "format": "date"}, "salesAmount": sales_money, "unitsSold": {"type": "integer", "minimum": 0}, "invoiceCount": {"type": "integer", "minimum": 0}}, {"date": "2026-11-20", "salesAmount": 2485, "unitsSold": 23, "invoiceCount": 1})
schema("SalesVariantList", {"items": arr(ref("SalesVariant")), **page_props}, {"items": [examples["SalesVariant"], {**examples["SalesVariant"], "variantId": "MVN-000002", "variantName": "2.5kg", "unitsSold": 5, "salesAmount": 325, "averageUnitPrice": 65}], "page": 1, "limit": 20, "total": 2})
schema("SalesPointList", {"items": arr(ref("SalesPoint")), **page_props}, {"items": [examples["SalesPoint"]], "page": 1, "limit": 31, "total": 1})
schema("SalesAnalytics", {"period": ref("SalesPeriod"), "filters": {"type": "object", "additionalProperties": string}, "groupBy": {**string, "enum": ["day", "month"]}, "summary": ref("SalesSummary"), "collections": ref("SalesCollections"), "collectionsScope": string, "products": ref("SalesVariantList"), "trend": ref("SalesPointList"), "generatedAt": timestamp}, {"period": examples["SalesPeriod"], "filters": {}, "groupBy": "day", "summary": examples["SalesSummary"], "collections": examples["SalesCollections"], "collectionsScope": "Customer receipts dated in this period; may settle older invoices or become advance credit. Not sales revenue.", "products": examples["SalesVariantList"], "trend": examples["SalesPointList"], "generatedAt": "2026-11-20T09:00:00Z"})
op = operation("GET", "/api/v1/analytics/sales", "Sales analytics by period, product, variant and customer", A, "SalesAnalytics", paginate=True, description="Admin/manager only, signed business scope. Read-only, single transaction snapshot. Sales use issued invoice creation dates and saved line amounts, never current variant prices, order quantities or pickup stock. Cancelled invoices are excluded; archived products, variants and customers remain in historical results. Product/variant filters count only matching invoice lines; invoiceCount/customerCount are distinct within the matching set, and averageInvoiceValue is matching sales divided by matching invoices. Collections are receipts dated within the period/customer, independent of invoice dates or payment allocation; they are omitted (null) when filtering a product/variant because receipts cannot be attributed to product lines. Filters apply before every total and pagination. Both date boundaries are inclusive Indian calendar dates. With no dates, defaults to this month through today in IST; dateTo alone uses the first day of that month, dateFrom alone uses today. Maximum 366 days. Variant must belong to selected product; foreign/missing references return NOT_FOUND. Products are ranked by sales descending then variantId, and trends are chronological with zero-sales periods included. Product rows and trend rows paginate independently; page/limit apply to products, trendPage/trendLimit to trend. Daily buckets use YYYY-MM-DD; monthly buckets use the first day of the month and partial boundary months contain only the selected dates. No stock or money is changed.")
op["responses"].pop("409", None)
op["parameters"] += [
    *[{"in": "query", "name": key, "schema": {**string, "format": "date"}, "example": "2026-11-20"} for key in ["dateFrom", "dateTo"]],
    *[{"in": "query", "name": key, "schema": {**string, "pattern": prefix + "[0-9]+$"}} for key, prefix in [("productId", "^MPN-"), ("variantId", "^MVN-"), ("customerId", "^CUS-")]],
    {"in": "query", "name": "groupBy", "schema": {**string, "enum": ["day", "month"], "default": "day"}},
    {"in": "query", "name": "trendPage", "schema": {"type": "integer", "minimum": 1, "maximum": 1000000, "default": 1}},
    {"in": "query", "name": "trendLimit", "schema": {"type": "integer", "minimum": 1, "maximum": 200, "default": 31}},
]
filtered_sales = copy.deepcopy(examples["SalesAnalytics"])
filtered_sales.update({"filters": {"productId": "MPN-000001"}, "collections": None, "collectionsScope": "Customer receipts cannot be attributed to a product or variant. Collections are omitted for product-filtered reports."})
empty_sales = copy.deepcopy(examples["SalesAnalytics"])
empty_sales["summary"] = {key: 0 for key in examples["SalesSummary"]}
empty_sales["collections"] = {key: 0 for key in examples["SalesCollections"]}
empty_sales["products"].update({"items": [], "total": 0})
empty_sales["trend"]["items"] = [{"date": "2026-11-20", "salesAmount": 0, "unitsSold": 0, "invoiceCount": 0}]
op["responses"]["200"]["content"]["application/json"].pop("example", None)
op["responses"]["200"]["content"]["application/json"]["examples"] = {"allProducts": {"value": examples["SalesAnalytics"]}, "productFilter": {"value": filtered_sales}, "noSales": {"value": empty_sales}}

for method, path, summary, response, request, status, roles in [
    ("POST", "/api/v1/coupons", "Create a coupon", "Coupon", "CouponInput", 201, A),
    ("PUT", "/api/v1/coupons/{id}", "Edit a coupon for future orders", "Coupon", "CouponInput", 200, A),
    ("GET", "/api/v1/coupons", "List business coupons", "CouponList", None, 200, A),
    ("GET", "/api/v1/coupons/options", "Choose an active coupon for an order", "CouponList", None, 200, ALL),
    ("POST", "/api/v1/coupons/{id}/archive", "Archive a coupon", "Archived", None, 200, A),
]:
    op = operation(method, path, summary, roles, response, request, status=status, paginate=method=="GET", description="Signed-business scope. Coupon IDs use the atomic CPN- series. Admins/managers manage coupons; all order creators may choose active coupons. Existing order discount snapshots are unaffected by edits or archiving. Archived coupon codes stay reserved. No stacking. Percentage 0 < value <= 100; fixed INR > 0. Two-decimal rounding.")
    if path == "/api/v1/coupons" and method == "GET":
        op["parameters"].append({"in": "query", "name": "isActive", "schema": {"type": "boolean"}})
    if method in ["POST", "PUT"] and request:
        op["responses"]["409"]["content"]["application/json"]["example"] = {"error": {"code": "COUPON_CODE_EXISTS", "message": "This coupon code already exists in your business"}}

operation("GET", "/health", "Liveness", [], "Health")
operation("POST", "/api/v1/auth/login", "Log in; paste accessToken into Authorize", [], "LoginResult", "LoginInput")
operation("POST", "/api/v1/auth/signup", "Register a business and its owner admin", [], "LoginResult", "SignupInput", status=201, example={**examples["LoginResult"], "user": {**admin, "name": "Arun", "username": "arun", "email": "arun@example.com", "phone": "9000000001"}}, description="Public admin signup only. Creates one independent business account and its owner atomically. Does not accept role/accountId. Usernames and emails are globally unique. Staff and riders are created by their business admin and use login only.")
operation("POST", "/api/v1/display-names", "Resolve readable labels for accessible business references", ALL, "DisplayNamesResult", "DisplayNamesInput", description="Read-only batch lookup of up to 200 explicit business IDs, not a directory/list endpoint. Returns names only; never stock, prices, customer contact details, balances or credentials. Missing/inaccessible references are omitted. Every query uses the signed business account. Riders may resolve customer names across their own business for order selection. Staff customer labels and all team labels are restricted to visible orders/trips and own identity; location/vehicle/order/trip restrictions match existing access. Riders resolve invoice numbers only on their own trips; warehouse staff cannot resolve invoices. Archived accessible names remain available for history. IDs remain the values submitted by clients.")
operation("GET", "/api/v1/auth/me", "Get logged-in user, account and assignments", ALL, "SessionProfile")
operation("GET", "/api/v1/account", "Get own business profile", ALL, "BusinessAccount")
operation("PUT", "/api/v1/account", "Replace own business profile", ["admin"], "BusinessAccount", "BusinessProfileInput")
operation("PUT", "/api/v1/trips/{id}/staff", "Assign pickup staff to a planned trip", A, "Trip", "TripStaffInput", description="Admin/manager only; users must be warehouse staff of the same business. Staff must be assigned to the trip. A nonempty user location assignment restricts pickup to those locations; an empty assignment allows any active storage location in the same business.")

operation("GET", "/api/v1/account/order-settings", "Read own business order and trip defaults", ALL, "OrderSettings", example={"autoAssignDeliveryPartnerOrders": False, "tripDurationDays": 1})
operation("PUT", "/api/v1/account/order-settings", "Configure automatic rider-order trips and duration", ["admin"], "OrderSettings", "OrderSettings", description="Defaults: auto assignment off, duration one Indian calendar day. Duration changes affect new trips only. Current started trips are preferred, then planned trips in the current window. If none exists, create today's trip using the rider's assigned vehicle. Stock changes only at pickup/delivery/return.")
operation("PUT", "/api/v1/users/{id}/delivery-vehicle", "Assign rider vehicle for automatic trips", ["admin"], "User", "DeliveryVehicleInput", example={**examples["User"], "userId": "USR-000003", "role": "deliveryPartner", "name": "Delivery Partner", "username": "arun.rider", "assignedVehicleId": "VEH-000001"})

resources = {"products": "Product", "variants": "Variant", "warehouses": "Warehouse", "locations": "Location", "vehicles": "Vehicle", "customers": "Customer", "users": "User", "orders": "Order", "trips": "Trip", "stocks": "Stock", "stockMovements": "Movement", "invoices": "Invoice", "payments": "Payment", "paymentAllocations": "Allocation", "settlements": "Settlement"}
for resource, model in resources.items():
    list_name = model + "List"
    schema(list_name, {"items": arr(ref(model)), **page_props}, {"items": [examples[model]], "page": 1, "limit": 20, "total": 1})
    roles = ALL if resource in ["products", "variants", "warehouses", "locations", "vehicles", "trips", "orders", "stocks"] else S if resource in ["orders", "stocks"] else A
    op = operation("GET", "/api/v1/" + resource, "List " + resource, roles, list_name, paginate=True)
    for key in ["warehouseId", "productId", "locationId", "variantId", "customerId", "tripId", "isActive"]:
        op["parameters"].append({"in": "query", "name": key, "schema": {"type": "boolean"} if key == "isActive" else string})
    if resource in ["locations", "warehouses", "stocks"]:
        op["description"] += " Warehouse staff and delivery partners with empty locationIds can browse all storage locations/warehouses in their own business; nonempty assignments restrict storage visibility. Storage stock excludes vehicle stock, which remains trip-scoped."
    if resource == "orders":
        op["description"] += " Filters combine with AND across every page: status, source, customerId, tripId, deliveryPartnerId and inclusive Indian calendar dateFrom/dateTo (creation date). Role/account restrictions still apply. Sort is newest createdAt then descending orderId. Invalid status/source/date or reversed dates returns VALIDATION_ERROR. Missing matches return an empty list."
        op["parameters"] += [
            {"in": "query", "name": "status", "schema": {**string, "enum": ["open", "assigned", "delivered", "partiallyDelivered", "failed", "cancelled"]}},
            {"in": "query", "name": "source", "schema": schemas["OrderInput"]["properties"]["source"]},
            {"in": "query", "name": "deliveryPartnerId", "schema": {**string, "pattern": "^USR-[0-9]+$"}},
            *[{"in": "query", "name": key, "description": "Inclusive Indian calendar creation date", "schema": {**string, "format": "date"}} for key in ["dateFrom", "dateTo"]],
        ]
        op["responses"]["200"]["content"]["application/json"]["examples"] = {"matchingOrders": {"value": examples[list_name]}, "noMatches": {"value": {"items": [], "page": 1, "limit": 20, "total": 0}}}
        op["responses"]["200"]["content"]["application/json"].pop("example", None)
    if resource == "trips":
        op["description"] += " Delivery partners receive only their own trips."
# Optional filters apply before pagination and always intersect account/role scope.
for resource, enums in {"trips": {"status": ["planned", "started", "completed", "settled"]}, "invoices": {"status": ["issued", "cancelled"]}, "payments": {"method": ["cash", "upi", "bank"]}, "stockMovements": {"kind": ["receive", "transfer", "adjustment", "pickup", "delivery", "return"]}, "users": {"role": ALL}}.items():
    op = paths["/api/v1/"+resource]["get"]
    op["parameters"] += [{"in": "query", "name": key, "schema": {**string, "enum": values}} for key, values in enums.items()]
for resource in ["trips", "invoices", "payments", "stockMovements", "settlements", "paymentAllocations"]:
    op = paths["/api/v1/"+resource]["get"]
    op["description"] += " Newest first. Filters combine before pagination; date bounds are inclusive Indian calendar creation dates. Invalid filter values return VALIDATION_ERROR."
    op["parameters"] += [{"in": "query", "name": key, "schema": {**string, "format": "date"}} for key in ["dateFrom", "dateTo"]]
for resource, keys in {"trips": ["deliveryPartnerId", "vehicleId"], "payments": ["receivedBy"], "settlements": ["hasDifference"]}.items():
    paths["/api/v1/"+resource]["get"]["parameters"] += [{"in": "query", "name": key, "schema": {"type": "boolean"} if key == "hasDifference" else string} for key in keys]
paths["/api/v1/stockMovements"]["get"]["description"] += " locationId matches either source or destination."
for resource in ["products", "variants", "warehouses", "locations", "vehicles", "customers", "users", "orders", "trips"]:
    model = resources[resource]
    operation("POST", "/api/v1/" + resource, "Create " + model.lower(), ["admin"] if resource == "users" else ALL if resource == "orders" else D if resource == "trips" else A, model, model + "Input", status=201)
schema("CustomerOptionList", {"items": arr(ref("CustomerOption")), **page_props}, {"items": [examples["CustomerOption"]], "page": 1, "limit": 20, "total": 1})
operation("GET", "/api/v1/customers/options", "Choose an active customer by name for a new order", D, "CustomerOptionList", paginate=True, description="Paginated active customer names and business IDs in the signed business account, sorted by name then customer ID. Delivery partners can choose a customer before any order or trip exists. No contacts, balances or global ledgers are returned. Customer creation remains admin/manager only.")
assign_op = operation("POST", "/api/v1/orders/{id}/assign", "Assign an open order to a delivery partner", A, "Order", "AssignOrderInput", example={**examples["Order"], "status": "assigned", "tripId": "TRP-000001"}, description="Admin/manager only. Atomically attaches an open, unpicked order to the selected active delivery partner's current-window trip. Started trips are preferred, then earliest planned trip; optional vehicleId restricts matching to that van. If none exists, creates today's planned trip using the selected or default vehicle and the business trip duration. Works even when automatic rider-order assignment is disabled. All references are scoped to the signed business. Assignment never moves stock. Already assigned/closed orders cannot be reassigned. An explicit tripId targets that trip instead of automatic selection; mismatched partner/vehicle is invalid. Missing vehicle or full trip rolls back the whole operation. Van use is claimed at pickup, so a new planned trip may wait for an earlier loaded trip. The partner then sees the assigned order in their own trip.")
assign_op["responses"]["409"]["content"]["application/json"]["examples"] = {"alreadyAssigned": {"value": {"error": {"code": "ORDER_ALREADY_ASSIGNED", "message": "Only an open, unpicked order can be assigned"}}}, "vehicleRequired": {"value": {"error": {"code": "DELIVERY_VEHICLE_NOT_ASSIGNED", "message": "Choose a vehicle or assign a default vehicle to the delivery partner before creating a trip"}}}}
assign_op["responses"]["409"]["content"]["application/json"].pop("example", None)
schema("AvailabilityList", {"items": arr(ref("Availability")), **page_props, "totals": ref("AvailabilityTotals")}, {"items": [examples["Availability"]], "page": 1, "limit": 20, "total": 1, "totals": availability_example})
operation("GET", "/api/v1/partners/cash", "Cash held and collections by delivery partner", A, "PartnerCashList", paginate=True, description="Paginated delivery partners in the signed business, including archived partners with outstanding obligations. Computed from trip payments, independent receipts recorded by this partner, actual trip cash handovers and immutable partner cash entries. Office counter collections are not attributed to partners. Total collections include cash/UPI/bank; only cash affects cashHeld. Over-handovers are shown separately. No editable balance.")
operation("GET", "/api/v1/partners/{id}/cash", "Partner cash ledger and salary adjustments", D, "PartnerCashLedger", paginate=True, description="Admin/manager may read any partner in their business. A partner may read only their own ledger. History is paginated newest first; summary totals cover all history. Includes note/reference, gross salary and computed salary payable where supplied. Customer invoices and balances are unaffected by cash settlement.")
operation("POST", "/api/v1/partners/{id}/cash-settlements", "Settle held cash by handover or salary adjustment with note", A, "PartnerCashResult", "PartnerCashInput", status=201, description="Transactional, append-only record. Note and requestId are required. Amount must not exceed cash currently held. Allocates to oldest outstanding cash sources first: trip cash or independent customer receipt. Each allocation names either tripId or paymentId, preserving original receipt and trip accounting. cashHandover records actual money returned; salaryAdjustment records offset against salary without claiming cash was handed over. Optional salaryDue yields salaryPayable = salaryDue - amount. No customer payment/invoice/allocation is changed. Reusing the same requestId and details returns the original entry without a second deduction; changed details return IDEMPOTENCY_CONFLICT. Shared partner locking prevents concurrent over-settlement. Archived partners can still be settled.")
paths["/api/v1/partners/{id}/cash-settlements"]["post"]["responses"]["409"]["content"]["application/json"]["example"] = {"error": {"code": "CASH_HOLDING_EXCEEDED", "message": "Settlement amount exceeds the cash currently held by this partner"}}
operation("GET", "/api/v1/trips/{id}/cash-summary", "Cash remaining after earlier handovers and salary adjustments", A, "TripCashSummary", description="Snapshot-consistent cash accounting for a trip in the signed business. remainingCash excludes digital payments and all recorded handovers/salary offsets. Use before final trip settlement to enter only the additional cash being handed over.")
operation("GET", "/api/v1/inventory/availability", "Storage stock: on hand, reserved, available and awaiting stock", A, "AvailabilityList", paginate=True, description="Snapshot-consistent computed inventory for active storage locations/warehouses and active variants in the signed business. Excludes vehicle stock. Totals cover all variants, not only the requested page. Open/assigned unpicked demand reserves oldest order first; shortages appear as awaitingStockQuantity. Existing orders are included. Physical quantity changes only through move(). Scoped storage rows are also available through GET /stocks with reservedQuantity and availableQuantity.")
operation("POST", "/api/v1/orders/bulk-assign", "Assign up to 200 open orders to one trip atomically", D, "BulkOrdersResult", "BulkAssignOrdersInput", description="All-or-nothing transaction. Admin/manager may choose a partner (reuse/create trip) or an explicit tripId. Riders must choose their own tripId and may attach only orders they created. A failed order, duplicate ID, mismatched partner/vehicle or trip capacity error rolls back every assignment and any newly created trip. Does not move physical stock or release reservations.")
operation("POST", "/api/v1/orders/bulk-cancel", "Cancel up to 200 open unassigned orders and release reservations", A, "BulkOrdersResult", "BulkCancelOrdersInput", example={"orders": [{**examples["Order"], "status": "cancelled", "reservedItems": [], "awaitingStockItems": [], "cancelReason": "Customer cancelled before assignment", "cancelledBy": "USR-000001", "cancelledAt": stamp}]}, description="All-or-nothing transaction. Only open, unassigned, unpicked orders can be cancelled. Requires a reason; records actor and time, keeps the orders, and releases their computed reservations. Assigned/picked orders return ORDER_NOT_CANCELLABLE. Never retries cash or delivery operations.")
paths["/api/v1/orders/bulk-cancel"]["post"]["responses"]["409"]["content"]["application/json"]["example"] = {"error": {"code": "ORDER_NOT_CANCELLABLE", "message": "Only open, unassigned and unpicked orders can be cancelled"}}
paths["/api/v1/trips"]["post"]["description"] = "Admin/manager may plan trips for any active partner/vehicle in their business. Delivery partners may create today's trips for themselves using a visible assigned/default vehicle and only their own open orders; cannot assign warehouse staff. Multiple trips on the same day, including the same van, are allowed. Planning does not claim stock or the van. First pickup claims exclusive vehicle use until completion. The van must be empty before another trip loads. Date/duration follow the business Indian calendar policy."
order_op = paths["/api/v1/orders"]["post"]
order_op["description"] = "Riders can create orders for customers in their own business. With autoAssignDeliveryPartnerOrders enabled, atomically add a pending stop to their current-window trip, or create a planned trip for today with the assigned vehicle. Started trips are preferred; remaining ties use earliest start/creation/ID. Missing vehicle or a full current trip fails the whole request without creating an orphan order. With the setting disabled, orders remain open. Creating an order never moves physical stock. Unpicked open/assigned quantities reserve storage stock oldest order first. With 225 on hand and an order of 13, reserved is 13 and available is 212. Orders exceeding storage stock remain allowed, with awaitingStockItems until stock arrives. Optional tripId chooses a specific own trip instead of automatic assignment. Existing orders participate immediately; no migration is needed."
order_op["responses"]["201"]["content"]["application/json"]["examples"] = {"manual": {"value": examples["Order"]}, "autoAssignedRider": {"value": {**examples["Order"], "source": "deliveryPartner", "createdBy": "USR-000003", "status": "assigned", "tripId": "TRP-000001"}}}
order_op["responses"]["201"]["content"]["application/json"].pop("example", None)
order_op["responses"]["409"]["content"]["application/json"]["example"] = {"error": {"code": "DELIVERY_VEHICLE_NOT_ASSIGNED", "message": "Choose a vehicle or assign a default vehicle to the delivery partner before creating a trip"}}
paths["/api/v1/orders"]["get"]["description"] += " Riders see only orders they created or orders on their own trips."
operation("PATCH", "/api/v1/variants/{id}/price", "Update global variant price", A, "Variant", "PriceInput")
assigned = {**examples["User"], "locationIds": ["LOC-000001"]}
location_op = operation("PUT", "/api/v1/users/{id}/locations", "Replace staff/partner location assignments", A, "User", "AssignmentInput", example=assigned, description="Admin/manager only. A nonempty locationIds list restricts staff/partner pickup to those locations. Send [] to allow pickup from any active storage location in this business. An empty list does not grant access to other trips or business accounts. Null or omitted locationIds is invalid. Existing login tokens use fresh assignments on every request.")
location_op["responses"]["200"]["content"]["application/json"]["examples"] = {"restricted": {"value": assigned}, "unrestricted": {"value": {**assigned, "locationIds": []}}}
location_op["responses"]["200"]["content"]["application/json"].pop("example", None)
for action, inp in [("receive", "ReceiveInput"), ("transfer", "TransferInput"), ("adjust", "AdjustmentInput")]:
    ex = examples["Movement"]
    if action == "transfer":
        ex = {**ex, "kind": "transfer", "fromLocationId": "LOC-000001", "toLocationId": "LOC-000002", "quantity": 5, "reason": "Storage transfer"}
    if action == "adjust":
        ex = {**ex, "kind": "adjustment", "fromLocationId": "LOC-000001", "quantity": 1, "reason": "Damaged bag"}
        ex.pop("toLocationId", None)
    operation("POST", "/api/v1/stock/" + action, action.capitalize() + " storage stock", D if action == "adjust" else ALL, "Movement", inp, status=201, example=ex, description="Storage locations only. Delivery partners can receive, transfer and adjust storage stock without a trip. Staff/partner source access is unrestricted when the location assignment is empty; otherwise it is limited to assigned locations. Partner transfers require access to both source and destination. Partner adjustments require a nonblank reason, and physical stock cannot become negative. Other reservations recompute after physical count corrections. Admin/manager may use any active storage location in their business. Vehicle stock changes only through trip operations.")
operation("GET", "/api/v1/trips/{id}", "Get trip; partners own trips only", ALL, "Trip")
operation("GET", "/api/v1/trips/{id}/orders", "Get orders on an accessible trip", ALL, "OrderList", paginate=True, example={"items": [picked_order], "page": 1, "limit": 20, "total": 1})
operation("GET", "/api/v1/trips/{id}/stock", "Get current stock on trip vehicle", ALL, "StockList", paginate=True, example={"items": [{"stockKey": "VEH-000001:MVN-000001", "locationId": "VEH-000001", "variantId": "MVN-000001", "quantity": 20, "reservedQuantity": 0, "availableQuantity": 0}], "page": 1, "limit": 20, "total": 1}, description="Own trip only for partners. Returns no current stock once the trip completes, so subsequent vehicle reuse cannot expose another trip inventory.")
operation("POST", "/api/v1/trips/{id}/pickup", "Pick order items into van", ALL, "PickupResult", "PickupInput", description="Planned or started trip only. Provide either orderId or a new order. Late orders can be picked after departure; goods still move from an authorized storage location into the van. Warehouse staff must be assigned to the trip. Staff/partners with no assigned locations can pick up from any active storage location in their business. With one or more assignments they can pick up only from those locations (LOCATION_NOT_ASSIGNED otherwise). Admin/manager may use any active storage location in their business. Missing/inactive/foreign locations remain unavailable. Pickup atomically consumes this order’s oldest-first reservation and claims exclusive vehicle use. It cannot consume stock reserved for older orders. Another loaded trip on this van returns VEHICLE_BUSY; unused planned trips may share a van. Partners may access only their own trip.")
paths["/api/v1/trips/{id}/pickup"]["post"]["responses"]["409"]["content"]["application/json"]["examples"] = {"restrictedLocation": {"value": {"error": {"code": "LOCATION_NOT_ASSIGNED", "message": "Pickup is limited to your assigned locations"}}}, "insufficientStock": {"value": {"error": {"code": "INSUFFICIENT_STOCK", "message": "Source location has insufficient stock"}}}}
paths["/api/v1/trips/{id}/pickup"]["post"]["responses"]["409"]["content"]["application/json"].pop("example", None)
paths["/api/v1/trips/{id}/pickup"]["post"]["responses"]["409"]["content"]["application/json"]["examples"]["busyVan"] = {"value": {"error": {"code": "VEHICLE_BUSY", "message": "Finish the van's loaded trip before picking another trip"}}}
started = {**examples["Trip"], "status": "started"}
operation("POST", "/api/v1/trips/{id}/start", "Start fully picked trip", D, "Trip", example=started)
operation("POST", "/api/v1/trips/{id}/stops/{orderId}/close", "Atomically close stop and invoice delivered goods", D, "CloseStopResult", "CloseStopInput", description="Close once. Empty delivered array means failure. Partial/failed delivery requires reason. Variant prices are read at delivery. Existing advance is allocated first, then payments oldest invoice first. Late orders with delivered quantities must be fully picked first (ORDER_NOT_FULLY_PICKED otherwise); empty failed deliveries remain possible. Leftovers stay on van until returned. Partners own trips only.")
schema("MovementArray", {}, {})
schemas["MovementArray"] = arr(ref("Movement"))
return_move = {**pickup_move, "fromLocationId": "VEH-000001", "toLocationId": "LOC-000001", "quantity": 2, "kind": "return", "referenceId": "TRP-000001", "reason": "Undelivered goods"}
operation("POST", "/api/v1/trips/{id}/returns", "Return leftovers after all stops close", ALL, "MovementArray", "ReturnInput", example=[return_move])
operation("POST", "/api/v1/trips/{id}/complete", "Complete trip; all stops closed and van empty", D, "Trip", example={**started, "status": "completed", "stops": [closed_stop]})
operation("POST", "/api/v1/trips/{id}/settle", "Record cash handover and flag difference", A, "Settlement", "SettlementInput", status=201, description="cashHandedOver is additional physical cash returned at this final trip settlement. Difference includes earlier partner cash handovers and salary offsets allocated to this trip. It does not count the same salary offset or handover twice. Read cash-summary first. Original collections and customer balances remain unchanged.")
operation("POST", "/api/v1/payments", "Collect payment; oldest invoices first", D, "Payment", "PaymentInput", status=201, description="All allowed roles, including delivery partners, may omit tripId to collect independently at any time from a customer in their signed business, without creating a trip/order/stock movement. receivedBy is always the authenticated actor. Independent cash is included in that partner’s cashHeld and can be settled by cash handover or salary adjustment; UPI/bank appears in collection totals but never cashHeld. If tripId is supplied, the trip must be started and the customer must be on it; partners may use only their own trip. Trip cash remains included in trip settlement. Overpayment becomes advance credit. Do not repeat a collection request without confirming whether it committed.")
schemas["PaymentInput"]["properties"]["tripId"]["description"] = "Optional. Omit for an independent collection, including delivery partner collections on a later visit. If supplied, requires an authorized started trip and a customer on that trip."
paths["/api/v1/payments"]["post"]["responses"]["201"]["content"]["application/json"]["examples"] = {"tripCollection": {"value": examples["Payment"]}, "independentPartnerCollection": {"value": {"paymentId": "PAY-000002", "customerId": "CUS-000001", "amount": 3500, "method": "cash", "receivedBy": "USR-000003", "createdAt": stamp}}}
paths["/api/v1/payments"]["post"]["responses"]["201"]["content"]["application/json"].pop("example", None)
paths["/api/v1/payments"]["post"]["requestBody"]["content"]["application/json"]["examples"] = {"independentCollection": {"value": {"customerId": "CUS-000001", "amount": 3500, "method": "cash"}}, "tripCollection": {"value": examples["PaymentInput"]}}
paths["/api/v1/payments"]["post"]["requestBody"]["content"]["application/json"].pop("example", None)
for route in ["/api/v1/customers/{id}/ledger/export", "/api/v1/trips/{id}/customers/{customerId}/ledger/export"]:
    operation("GET", route, "Export customer ledger as a shareable PDF", D if "/trips/" in route else A, "LedgerDocument", description="Authenticated export using the same permissions as the customer ledger. Riders require their own planned/started trip and a customer on that trip. One consistent database snapshot includes every matching transaction, newest first, with all-time balances, business/customer names and an IST generation timestamp. Up to 10000 matching entries; larger exports return VALIDATION_ERROR and require a date filter. Returns base64 PDF bytes, never a public link; does not send messages or create financial records.")
operation("GET", "/api/v1/customers/{id}/ledger", "Computed customer balance and paginated entries", A, "Ledger", paginate=True)
operation("GET", "/api/v1/trips/{id}/customers/{customerId}/ledger", "Ledger for customer on own planned/started trip", D, "Ledger", paginate=True)
operation("GET", "/api/v1/invoices/{id}", "Get invoice; partners own trips only", D, "Invoice")
operation("POST", "/api/v1/invoices/{id}/cancel", "Cancel completely unpaid invoice and retain number", A, "Invoice", "ReasonInput", example={**examples["Invoice"], "status": "cancelled", "isActive": False, "cancelReason": "Invoice issued in error"}, description="Cancellation does not move stock. Record any physical returns separately. Any allocation, including advance credit, prevents cancellation.")
for resource in ["products", "variants", "customers", "users"]:
    operation("POST", "/api/v1/" + resource + "/{id}/archive", "Archive " + resources[resource].lower(), ["admin"] if resource == "users" else A, "Archived")
for path, mime in [("/docs", "text/html"), ("/docs/", "text/html"), ("/docs/openapi.yaml", "application/yaml")]:
    paths[path] = {"get": {"summary": "Public API documentation", "x-roles": [], "security": [], "responses": {"200": {"description": "Embedded documentation", "content": {mime: {"schema": string, "example": "Harbor API documentation"}}}}}}

for route in ["/api/v1/customers/{id}/ledger", "/api/v1/trips/{id}/customers/{customerId}/ledger", "/api/v1/customers/{id}/ledger/export", "/api/v1/trips/{id}/customers/{customerId}/ledger/export", "/api/v1/partners/{id}/cash"]:
    partner = "/partners/" in route
    op = paths[route]["get"]
    op["description"] += " History is newest createdAt then descending entryId; totals always include all history, even when filtered. Payment allocation remains oldest invoice first."
    op["parameters"] += [
        {"in": "query", "name": "type", "schema": {**string, "enum": ["collection", "tripHandover", "cashHandover", "salaryAdjustment"] if partner else ["invoice", "payment", "cancelledInvoice"]}},
        {"in": "query", "name": "method", "schema": {**string, "enum": ["cash", "upi", "bank", "cashHandover", "salaryAdjustment"] if partner else ["cash", "upi", "bank"]}},
        *[{"in": "query", "name": key, "schema": {**string, "format": "date"}, "description": "Inclusive Indian calendar date"} for key in ["dateFrom", "dateTo"]],
    ]

discount_description = " Optional couponId OR manual discount, never both. Admin/manager manual discounts require a reason; staff/riders can apply only active pre-approved coupons. Terms are snapshotted on order creation. Fixed discounts cannot exceed the current order subtotal. Delivery retains current variant prices: percentage applies to delivered subtotal; fixed discount is prorated by delivered subtotal / full-order subtotal at those same delivery-time prices, capped to prevent a negative invoice. Undelivered goods receive no discount or invoice. Proportional line allocation uses largest remainders so cents sum exactly. Payments, ledger, advance and sales analytics use net totals."
paths["/api/v1/orders"]["post"]["description"] += discount_description
paths["/api/v1/trips/{id}/pickup"]["post"]["description"] += " Embedded order creation supports the same coupon/manual discount rules."
paths["/api/v1/trips/{id}/stops/{orderId}/close"]["post"]["description"] += discount_description
paths["/api/v1/analytics/sales"]["get"]["description"] += " Sales and average prices are net of invoice discounts; collection amounts remain actual receipts."
discounted_invoice = copy.deepcopy(examples["Invoice"])
discounted_invoice.update({"subtotal": 2485, "discountAmount": 248.5, "total": 2236.5, "discount": examples["Discount"]})
for line in discounted_invoice["lines"]:
    line["discountAmount"] = round(line["subtotal"] * 0.1, 2)
    line["amount"] = round(line["subtotal"] - line["discountAmount"], 2)
invoice_response = paths["/api/v1/invoices/{id}"]["get"]["responses"]["200"]["content"]["application/json"]
invoice_response.pop("example",None)
invoice_response["examples"] = {"standard": {"value": examples["Invoice"]}, "percentageCoupon": {"value": discounted_invoice}}
order_request = paths["/api/v1/orders"]["post"]["requestBody"]["content"]["application/json"]
order_request.pop("example", None)
order_request["examples"] = {"standard": {"value": examples["OrderInput"]}, "coupon": {"value": {**examples["OrderInput"], "couponId": "CPN-000001"}}, "manualFixed": {"value": {**examples["OrderInput"], "discount": examples["DiscountInput"]}}, "manualPercentage": {"value": {**examples["OrderInput"], "discount": {"type": "percentage", "value": 10, "reason": "Loyal customer"}}}}

spec = {"openapi": "3.0.3", "info": {"title": "Harbor by Code Wayne", "version": "1.11.0", "description": "Business account isolation is enforced from the signed JWT accountId on every protected operation. Do not send a tenant selector. Staff see all storage locations when their location assignment is empty, otherwise assigned locations; trips remain assignment-scoped; riders see their own trips. Warehouse, inventory and delivery backend. Quantities are whole units. Currency is INR; amounts round to two decimals. All stock and money changes are transactional. See README.md for the acceptance walkthrough, including the required 2.5kg stock receipt."}, "servers": [{"url": "/"}], "paths": paths, "components": {"securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}}, "schemas": schemas}}
(ROOT / "internal/docs/openapi.yaml").write_text(json.dumps(spec, indent=2) + "\n")
print(f"Wrote {sum(len(v) for v in paths.values())} operations and {len(schemas)} schemas")
