#!/usr/bin/env python3
"""Regenerate the checked-in OpenAPI contract (JSON is a YAML 1.2 subset)."""
import copy
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
schema("AssignmentInput", {"locationIds": arr(string)}, {"locationIds": ["LOC-000001"]})
schema("LoginInput", {"username": string, "password": {"type": "string", "format": "password"}}, {"username": "admin", "password": "Your-ADMIN_PASSWORD"})
admin = {**examples["User"], "userId": "USR-000001", "name": "Administrator", "username": "admin", "role": "admin"}
schema("LoginResult", {"accessToken": string, "tokenType": string, "expiresIn": {"type": "integer"}, "user": ref("User"), "account": ref("BusinessAccount")}, {"accessToken": "<signed-HS256-JWT>", "tokenType": "Bearer", "expiresIn": 28800, "user": admin, "account": examples["BusinessAccount"]})
schema("SessionProfile", {"user": ref("User"), "account": ref("BusinessAccount")}, {"user": admin, "account": examples["BusinessAccount"]})
schema("TripStaffInput", {"warehouseStaffIds": arr(string)}, {"warehouseStaffIds": ["USR-000002"]})
schema("ReceiveInput", {"locationId": string, "variantId": string, "quantity": quantity, "reason": text}, {"locationId": "LOC-000001", "variantId": "MVN-000001", "quantity": 100, "reason": "Opening stock"}, ["locationId", "variantId", "quantity"])
schema("TransferInput", {"fromLocationId": string, "toLocationId": string, "variantId": string, "quantity": quantity, "reason": text}, {"fromLocationId": "LOC-000001", "toLocationId": "LOC-000002", "variantId": "MVN-000001", "quantity": 5, "reason": "Storage transfer"}, ["fromLocationId", "toLocationId", "variantId", "quantity"])
schema("AdjustmentInput", {"locationId": string, "variantId": string, "quantityDelta": {"type": "integer", "minimum": -1_000_000, "maximum": 1_000_000, "description": "Must be nonzero"}, "reason": text}, {"locationId": "LOC-000001", "variantId": "MVN-000001", "quantityDelta": -1, "reason": "Damaged bag"})
schema("Movement", {"movementId": string, "variantId": string, "quantity": quantity, "fromLocationId": string, "toLocationId": string, "kind": string, "referenceId": string, "tripId": string, "reason": text, "actorId": string, "createdAt": timestamp}, {"movementId": "MOV-000001", "variantId": "MVN-000001", "quantity": 100, "toLocationId": "LOC-000001", "kind": "receive", "reason": "Opening stock", "actorId": "USR-000001", "createdAt": stamp}, ["movementId", "variantId", "quantity", "kind", "reason", "actorId", "createdAt"])
schema("Stock", {"stockKey": string, "locationId": string, "variantId": string, "quantity": {"type": "integer", "format": "int64", "minimum": 0}}, {"stockKey": "LOC-000001:MVN-000001", "locationId": "LOC-000001", "variantId": "MVN-000001", "quantity": 100})
schema("OrderInput", {"customerId": string, "source": {"type": "string", "enum": ["counter", "salesStaff", "store", "phone", "pickup", "deliveryPartner"]}, "items": {**items, "minItems": 1}}, {"customerId": "CUS-000001", "source": "phone", "items": [{"variantId": "MVN-000001", "quantity": 20}, {"variantId": "MVN-000002", "quantity": 5}]})
schemas["OrderInput"]["required"] = ["customerId", "items"]
schemas["OrderInput"]["properties"]["source"]["description"] = "Delivery partners may omit source (defaults to deliveryPartner). Other roles must supply it."
schema("Order", {"orderId": string, **schemas["OrderInput"]["properties"], "picked": items, "tripId": string, "status": string, "createdBy": string}, {"orderId": "ORD-000001", **examples["OrderInput"], "picked": [], "status": "open", "createdBy": "USR-000001"}, [*base, "orderId", "customerId", "source", "items", "picked", "status", "createdBy"], entity=True)
schema("AssignOrderInput", {"deliveryPartnerId": string, "vehicleId": {**string, "description": "Optional specific vehicle. Otherwise reuse the partner's current trip, or create a trip using their default vehicle."}}, {"deliveryPartnerId": "USR-000003", "vehicleId": "VEH-000001"}, ["deliveryPartnerId"])
schema("Stop", {"orderId": string, "status": {"type": "string", "enum": ["pending", "delivered", "partiallyDelivered", "failed"]}, "delivered": items, "invoiceId": string, "reason": text, "closedAt": timestamp}, {"orderId": "ORD-000001", "status": "pending", "delivered": []}, ["orderId", "status", "delivered"])
schema("TripInput", {"startDate": {**string, "format": "date", "description": "Optional Indian calendar start date; defaults to today. Duration snapshots the business setting."}, "vehicleId": string, "deliveryPartnerId": string, "orderIds": arr(string), "warehouseStaffIds": arr(string)}, {"vehicleId": "VEH-000001", "deliveryPartnerId": "USR-000003", "orderIds": ["ORD-000001"], "warehouseStaffIds": ["USR-000002"]}, ["vehicleId", "deliveryPartnerId"])
schema("Trip", {"tripDate": {**string, "format": "date"}, "durationDays": {"type": "integer", "minimum": 1, "maximum": 365}, "startsAt": timestamp, "endsAt": timestamp, "warehouseStaffIds": arr(string), "tripId": string, "vehicleId": string, "deliveryPartnerId": string, "status": {"type": "string", "enum": ["planned", "started", "completed", "settled"]}, "stops": arr(ref("Stop"))}, {"tripDate": "2026-11-19", "durationDays": 1, "startsAt": "2026-11-18T18:30:00Z", "endsAt": "2026-11-19T18:30:00Z", "warehouseStaffIds": ["USR-000002"], "tripId": "TRP-000001", "vehicleId": "VEH-000001", "deliveryPartnerId": "USR-000003", "status": "planned", "stops": [examples["Stop"]]}, entity=True)
schema("PickupInput", {"orderId": string, "order": ref("OrderInput"), "locationId": string, "items": {**items, "minItems": 1}}, {"orderId": "ORD-000001", "locationId": "LOC-000001", "items": examples["OrderInput"]["items"]}, ["locationId", "items"])
schemas["PickupInput"]["oneOf"] = [{"required": ["orderId"], "not": {"required": ["order"]}}, {"required": ["order"], "not": {"required": ["orderId"]}}]
picked_order = {**examples["Order"], "tripId": "TRP-000001", "picked": examples["OrderInput"]["items"], "status": "assigned"}
pickup_move = {**examples["Movement"], "quantity": 20, "fromLocationId": "LOC-000001", "toLocationId": "VEH-000001", "kind": "pickup", "referenceId": "ORD-000001", "tripId": "TRP-000001", "reason": ""}
schema("PickupResult", {"tripId": string, "order": ref("Order"), "movements": arr(ref("Movement"))}, {"tripId": "TRP-000001", "order": picked_order, "movements": [pickup_move, {**pickup_move, "movementId": "MOV-000002", "variantId": "MVN-000002", "quantity": 5}]})
method = {"type": "string", "enum": ["cash", "upi", "bank"]}
delivered = [{"variantId": "MVN-000001", "quantity": 18}, {"variantId": "MVN-000002", "quantity": 5}]
schema("CloseStopInput", {"delivered": items, "reason": text, "collectedAmount": amount, "paymentMethod": method}, {"delivered": delivered, "reason": "Two bags not accepted", "collectedAmount": 3500, "paymentMethod": "cash"}, ["delivered"])
line_props = {"variantId": string, "name": string, "quantity": quantity, "unitPrice": amount, "amount": amount}
schema("InvoiceLine", line_props, {"variantId": "MVN-000001", "name": "5kg", "quantity": 18, "unitPrice": 120, "amount": 2160})
invoice = {"invoiceId": "INV-000001", "invoiceNo": "INV/26-27/00001", "customerId": "CUS-000001", "orderId": "ORD-000001", "tripId": "TRP-000001", "lines": [examples["InvoiceLine"], {"variantId": "MVN-000002", "name": "2.5kg", "quantity": 5, "unitPrice": 65, "amount": 325}], "total": 2485, "status": "issued", "createdBy": "USR-000003"}
schema("Invoice", {"invoiceId": string, "invoiceNo": {"type": "string", "maxLength": 16, "pattern": "^INV/[0-9]{2}-[0-9]{2}/[0-9]{5}$"}, "customerId": string, "orderId": string, "tripId": string, "lines": arr(ref("InvoiceLine")), "total": amount, "status": {"type": "string", "enum": ["issued", "cancelled"]}, "cancelReason": text, "createdBy": string}, invoice, [*base, *invoice], entity=True)
schema("PaymentInput", {"customerId": string, "tripId": string, "amount": {**amount, "minimum": 0.01}, "method": method}, {"customerId": "CUS-000001", "tripId": "TRP-000001", "amount": 3500, "method": "cash"}, ["customerId", "amount", "method"])
payment = {"paymentId": "PAY-000001", "customerId": "CUS-000001", "tripId": "TRP-000001", "amount": 3500, "method": "cash", "receivedBy": "USR-000003", "createdAt": stamp}
schema("Payment", {"paymentId": string, **schemas["PaymentInput"]["properties"], "receivedBy": string, "createdAt": timestamp}, payment, ["paymentId", "customerId", "amount", "method", "receivedBy", "createdAt"])
schema("Allocation", {"allocationKey": string, "customerId": string, "paymentId": string, "invoiceId": string, "amount": amount, "createdAt": timestamp}, {"allocationKey": "PAY-000001:INV-000001", "customerId": "CUS-000001", "paymentId": "PAY-000001", "invoiceId": "INV-000001", "amount": 2485, "createdAt": stamp})
closed_stop = {"orderId": "ORD-000001", "status": "partiallyDelivered", "delivered": delivered, "invoiceId": "INV-000001", "reason": "Two bags not accepted", "closedAt": stamp}
schema("CloseStopResult", {"stop": ref("Stop"), "invoice": ref("Invoice"), "payment": ref("Payment")}, {"stop": closed_stop, "invoice": examples["Invoice"], "payment": payment}, ["stop"])
schema("ReturnInput", {"locationId": string, "items": {**items, "minItems": 1}, "reason": text}, {"locationId": "LOC-000001", "items": [{"variantId": "MVN-000001", "quantity": 2}], "reason": "Undelivered goods"}, ["locationId", "items"])
schema("SettlementInput", {"cashHandedOver": amount, "note": text}, {"cashHandedOver": 3450, "note": "50 short"}, ["cashHandedOver"])
schema("Settlement", {"tripId": string, "cashCollected": amount, "cashHandedOver": amount, "difference": {"type": "number", "format": "double", "description": "cashHandedOver minus cashCollected; negative means shortage"}, "hasDifference": {"type": "boolean"}, "note": text, "settledBy": string, "createdAt": timestamp}, {"tripId": "TRP-000001", "cashCollected": 3500, "cashHandedOver": 3450, "difference": -50, "hasDifference": True, "note": "50 short", "settledBy": "USR-000001", "createdAt": stamp})
schema("LedgerEntry", {"entryId": string, "type": string, "invoiceNo": string, "amount": {"type": "number"}, "allocated": amount, "outstanding": amount, "createdAt": timestamp}, {"entryId": "INV-000001", "type": "invoice", "invoiceNo": "INV/26-27/00001", "amount": 2485, "allocated": 2485, "outstanding": 0, "createdAt": stamp}, ["entryId", "type", "amount", "allocated", "outstanding", "createdAt"])
page_props = {"page": {"type": "integer", "minimum": 1}, "limit": {"type": "integer", "minimum": 1, "maximum": 200}, "total": {"type": "integer", "minimum": 0}}
schema("Ledger", {"customer": ref("Customer"), "invoiceTotal": amount, "paymentTotal": amount, "outstanding": amount, "advanceCredit": amount, "balance": {"type": "number", "description": "invoiceTotal minus paymentTotal; negative means customer credit"}, "entries": arr(ref("LedgerEntry")), **page_props}, {"customer": examples["Customer"], "invoiceTotal": 2485, "paymentTotal": 3500, "outstanding": 0, "advanceCredit": 1015, "balance": -1015, "entries": [examples["LedgerEntry"], {"entryId": "PAY-000001", "type": "payment", "amount": -3500, "allocated": 2485, "outstanding": 0, "createdAt": stamp}], "page": 1, "limit": 20, "total": 2})
schema("ReasonInput", {"reason": text}, {"reason": "Invoice issued in error"})
schema("Archived", {"isActive": {"type": "boolean", "enum": [False]}}, {"isActive": False})
schema("DisplayNamesInput", {"ids": {"type": "array", "maxItems": 200, "items": {"type": "string", "maxLength": 64, "pattern": "^(MPN|MVN|WHS|LOC|VEH|CUS|USR|ORD|TRP|INV|PAY|MOV)-[0-9]+$"}}}, {"ids": ["MVN-000001", "LOC-000001", "CUS-000001", "VEH-000001"]})
schema("DisplayNamesResult", {"names": {"type": "object", "additionalProperties": {"type": "string"}}}, {"names": {"MVN-000001": "ICE · 5kg", "LOC-000001": "Main Freezer · Kahakootam", "CUS-000001": "Sunrise Stores", "VEH-000001": "Delivery Van 1"}})
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


operation("GET", "/health", "Liveness", [], "Health")
operation("POST", "/api/v1/auth/login", "Log in; paste accessToken into Authorize", [], "LoginResult", "LoginInput")
operation("POST", "/api/v1/auth/signup", "Register a business and its owner admin", [], "LoginResult", "SignupInput", status=201, example={**examples["LoginResult"], "user": {**admin, "name": "Arun", "username": "arun", "email": "arun@example.com", "phone": "9000000001"}}, description="Public admin signup only. Creates one independent business account and its owner atomically. Does not accept role/accountId. Usernames and emails are globally unique. Staff and riders are created by their business admin and use login only.")
operation("POST", "/api/v1/display-names", "Resolve readable labels for accessible business references", ALL, "DisplayNamesResult", "DisplayNamesInput", description="Read-only batch lookup of up to 200 explicit business IDs, not a directory/list endpoint. Returns names only; never stock, prices, customer contact details, balances or credentials. Missing/inaccessible references are omitted. Every query uses the signed business account. Riders may resolve customer names across their own business for order selection. Staff customer labels and all team labels are restricted to visible orders/trips and own identity; location/vehicle/order/trip restrictions match existing access. Riders resolve invoice numbers only on their own trips; warehouse staff cannot resolve invoices. Archived accessible names remain available for history. IDs remain the values submitted by clients.")
operation("GET", "/api/v1/auth/me", "Get logged-in user, account and assignments", ALL, "SessionProfile")
operation("GET", "/api/v1/account", "Get own business profile", ALL, "BusinessAccount")
operation("PUT", "/api/v1/account", "Replace own business profile", ["admin"], "BusinessAccount", "BusinessProfileInput")
operation("PUT", "/api/v1/trips/{id}/staff", "Assign pickup staff to a planned trip", A, "Trip", "TripStaffInput", description="Admin/manager only; users must be warehouse staff of the same business. A staff pickup requires both trip assignment and source location assignment.")

operation("GET", "/api/v1/account/order-settings", "Read own business order and trip defaults", ALL, "OrderSettings", example={"autoAssignDeliveryPartnerOrders": False, "tripDurationDays": 1})
operation("PUT", "/api/v1/account/order-settings", "Configure automatic rider-order trips and duration", ["admin"], "OrderSettings", "OrderSettings", description="Defaults: auto assignment off, duration one Indian calendar day. Duration changes affect new trips only. Current started trips are preferred, then planned trips in the current window. If none exists, create today's trip using the rider's assigned vehicle. Stock changes only at pickup/delivery/return.")
operation("PUT", "/api/v1/users/{id}/delivery-vehicle", "Assign rider vehicle for automatic trips", ["admin"], "User", "DeliveryVehicleInput", example={**examples["User"], "userId": "USR-000003", "role": "deliveryPartner", "name": "Delivery Partner", "username": "arun.rider", "assignedVehicleId": "VEH-000001"})

resources = {"products": "Product", "variants": "Variant", "warehouses": "Warehouse", "locations": "Location", "vehicles": "Vehicle", "customers": "Customer", "users": "User", "orders": "Order", "trips": "Trip", "stocks": "Stock", "stockMovements": "Movement", "invoices": "Invoice", "payments": "Payment", "paymentAllocations": "Allocation", "settlements": "Settlement"}
for resource, model in resources.items():
    list_name = model + "List"
    schema(list_name, {"items": arr(ref(model)), **page_props}, {"items": [examples[model]], "page": 1, "limit": 20, "total": 1})
    roles = ALL if resource in ["products", "variants", "warehouses", "locations", "vehicles", "trips", "orders"] else S if resource in ["orders", "stocks"] else A
    op = operation("GET", "/api/v1/" + resource, "List " + resource, roles, list_name, paginate=True)
    for key in ["warehouseId", "productId", "locationId", "variantId", "customerId", "tripId", "isActive"]:
        op["parameters"].append({"in": "query", "name": key, "schema": {"type": "boolean"} if key == "isActive" else string})
    if resource == "trips":
        op["description"] += " Delivery partners receive only their own trips."
for resource in ["products", "variants", "warehouses", "locations", "vehicles", "customers", "users", "orders", "trips"]:
    model = resources[resource]
    operation("POST", "/api/v1/" + resource, "Create " + model.lower(), ["admin"] if resource == "users" else ALL if resource == "orders" else A, model, model + "Input", status=201)
schema("CustomerOptionList", {"items": arr(ref("CustomerOption")), **page_props}, {"items": [examples["CustomerOption"]], "page": 1, "limit": 20, "total": 1})
operation("GET", "/api/v1/customers/options", "Choose an active customer by name for a new order", D, "CustomerOptionList", paginate=True, description="Paginated active customer names and business IDs in the signed business account, sorted by name then customer ID. Delivery partners can choose a customer before any order or trip exists. No contacts, balances or global ledgers are returned. Customer creation remains admin/manager only.")
assign_op = operation("POST", "/api/v1/orders/{id}/assign", "Assign an open order to a delivery partner", A, "Order", "AssignOrderInput", example={**examples["Order"], "status": "assigned", "tripId": "TRP-000001"}, description="Admin/manager only. Atomically attaches an open, unpicked order to the selected active delivery partner's current-window trip. Started trips are preferred, then earliest planned trip; optional vehicleId restricts matching to that van. If none exists, creates today's planned trip using the selected or default vehicle and the business trip duration. Works even when automatic rider-order assignment is disabled. All references are scoped to the signed business. Assignment never moves stock. Already assigned/closed orders cannot be reassigned. Missing vehicle, busy/nonempty van or full trip rolls back the whole operation. The partner then sees the assigned order in their own trip.")
assign_op["responses"]["409"]["content"]["application/json"]["examples"] = {"alreadyAssigned": {"value": {"error": {"code": "ORDER_ALREADY_ASSIGNED", "message": "Only an open, unpicked order can be assigned"}}}, "vehicleRequired": {"value": {"error": {"code": "DELIVERY_VEHICLE_NOT_ASSIGNED", "message": "Choose a vehicle or assign a default vehicle to the delivery partner before creating a trip"}}}, "vehicleBusy": {"value": {"error": {"code": "VEHICLE_BUSY", "message": "Vehicle is already assigned to an unfinished trip"}}}}
assign_op["responses"]["409"]["content"]["application/json"].pop("example", None)
order_op = paths["/api/v1/orders"]["post"]
order_op["description"] = "Riders can create orders for customers in their own business. With autoAssignDeliveryPartnerOrders enabled, atomically add a pending stop to their current-window trip, or create a planned trip for today with the assigned vehicle. Started trips are preferred; remaining ties use earliest start/creation/ID. Missing vehicle, reserved van or a full current trip fails the whole request without creating an orphan order. With the setting disabled, orders remain open. Creating an order never moves stock."
order_op["responses"]["201"]["content"]["application/json"]["examples"] = {"manual": {"value": examples["Order"]}, "autoAssignedRider": {"value": {**examples["Order"], "source": "deliveryPartner", "createdBy": "USR-000003", "status": "assigned", "tripId": "TRP-000001"}}}
order_op["responses"]["201"]["content"]["application/json"].pop("example", None)
order_op["responses"]["409"]["content"]["application/json"]["example"] = {"error": {"code": "DELIVERY_VEHICLE_NOT_ASSIGNED", "message": "Choose a vehicle or assign a default vehicle to the delivery partner before creating a trip"}}
paths["/api/v1/orders"]["get"]["description"] += " Riders see only orders they created or orders on their own trips."
operation("PATCH", "/api/v1/variants/{id}/price", "Update global variant price", A, "Variant", "PriceInput")
assigned = {**examples["User"], "locationIds": ["LOC-000001"]}
operation("PUT", "/api/v1/users/{id}/locations", "Replace staff/partner location assignments", A, "User", "AssignmentInput", example=assigned)
for action, inp in [("receive", "ReceiveInput"), ("transfer", "TransferInput"), ("adjust", "AdjustmentInput")]:
    ex = examples["Movement"]
    if action == "transfer":
        ex = {**ex, "kind": "transfer", "fromLocationId": "LOC-000001", "toLocationId": "LOC-000002", "quantity": 5, "reason": "Storage transfer"}
    if action == "adjust":
        ex = {**ex, "kind": "adjustment", "fromLocationId": "LOC-000001", "quantity": 1, "reason": "Damaged bag"}
        ex.pop("toLocationId", None)
    operation("POST", "/api/v1/stock/" + action, action.capitalize() + " storage stock", A if action == "adjust" else S, "Movement", inp, status=201, example=ex, description="Storage locations only. Pickups require assignment unless admin or manager. Vehicle stock changes only through trip operations.")
operation("GET", "/api/v1/trips/{id}", "Get trip; partners own trips only", ALL, "Trip")
operation("GET", "/api/v1/trips/{id}/orders", "Get orders on an accessible trip", ALL, "OrderList", paginate=True, example={"items": [picked_order], "page": 1, "limit": 20, "total": 1})
operation("GET", "/api/v1/trips/{id}/stock", "Get current stock on trip vehicle", ALL, "StockList", paginate=True, example={"items": [{"stockKey": "VEH-000001:MVN-000001", "locationId": "VEH-000001", "variantId": "MVN-000001", "quantity": 20}], "page": 1, "limit": 20, "total": 1}, description="Own trip only for partners. Returns no current stock once the trip completes, so subsequent vehicle reuse cannot expose another trip inventory.")
operation("POST", "/api/v1/trips/{id}/pickup", "Pick order items into van", ALL, "PickupResult", "PickupInput", description="Planned or started trip only. Provide either orderId or a new order. Late orders can be picked after departure; goods still move from an authorized storage location into the van. Warehouse staff must be assigned to the trip and source location. Other pickups require source assignment unless admin/manager. Partners may access only their own trip.")
started = {**examples["Trip"], "status": "started"}
operation("POST", "/api/v1/trips/{id}/start", "Start fully picked trip", D, "Trip", example=started)
operation("POST", "/api/v1/trips/{id}/stops/{orderId}/close", "Atomically close stop and invoice delivered goods", D, "CloseStopResult", "CloseStopInput", description="Close once. Empty delivered array means failure. Partial/failed delivery requires reason. Variant prices are read at delivery. Existing advance is allocated first, then payments oldest invoice first. Late orders with delivered quantities must be fully picked first (ORDER_NOT_FULLY_PICKED otherwise); empty failed deliveries remain possible. Leftovers stay on van until returned. Partners own trips only.")
schema("MovementArray", {}, {})
schemas["MovementArray"] = arr(ref("Movement"))
return_move = {**pickup_move, "fromLocationId": "VEH-000001", "toLocationId": "LOC-000001", "quantity": 2, "kind": "return", "referenceId": "TRP-000001", "reason": "Undelivered goods"}
operation("POST", "/api/v1/trips/{id}/returns", "Return leftovers after all stops close", ALL, "MovementArray", "ReturnInput", example=[return_move])
operation("POST", "/api/v1/trips/{id}/complete", "Complete trip; all stops closed and van empty", D, "Trip", example={**started, "status": "completed", "stops": [closed_stop]})
operation("POST", "/api/v1/trips/{id}/settle", "Record cash handover and flag difference", A, "Settlement", "SettlementInput", status=201)
operation("POST", "/api/v1/payments", "Collect payment; oldest invoices first", D, "Payment", "PaymentInput", status=201, description="Partners must supply their own started trip and a customer on that trip. Trip cash is included in settlement. Non-trip collections are admin/manager only. Overpayment becomes advance credit. Do not repeat a collection request without confirming whether it committed.")
operation("GET", "/api/v1/customers/{id}/ledger", "Computed customer balance and paginated entries", A, "Ledger", paginate=True)
operation("GET", "/api/v1/trips/{id}/customers/{customerId}/ledger", "Ledger for customer on own planned/started trip", D, "Ledger", paginate=True)
operation("GET", "/api/v1/invoices/{id}", "Get invoice; partners own trips only", D, "Invoice")
operation("POST", "/api/v1/invoices/{id}/cancel", "Cancel completely unpaid invoice and retain number", A, "Invoice", "ReasonInput", example={**examples["Invoice"], "status": "cancelled", "isActive": False, "cancelReason": "Invoice issued in error"}, description="Cancellation does not move stock. Record any physical returns separately. Any allocation, including advance credit, prevents cancellation.")
for resource in ["products", "variants", "customers", "users"]:
    operation("POST", "/api/v1/" + resource + "/{id}/archive", "Archive " + resources[resource].lower(), ["admin"] if resource == "users" else A, "Archived")
for path, mime in [("/docs", "text/html"), ("/docs/", "text/html"), ("/docs/openapi.yaml", "application/yaml")]:
    paths[path] = {"get": {"summary": "Public API documentation", "x-roles": [], "security": [], "responses": {"200": {"description": "Embedded documentation", "content": {mime: {"schema": string, "example": "Harbor API documentation"}}}}}}

spec = {"openapi": "3.0.3", "info": {"title": "Harbor by Code Wayne", "version": "1.4.0", "description": "Business account isolation is enforced from the signed JWT accountId on every protected operation. Do not send a tenant selector. Staff see assigned locations and trips; riders see their own trips. Warehouse, inventory and delivery backend. Quantities are whole units. Currency is INR; amounts round to two decimals. All stock and money changes are transactional. See README.md for the acceptance walkthrough, including the required 2.5kg stock receipt."}, "servers": [{"url": "/"}], "paths": paths, "components": {"securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}}, "schemas": schemas}}
(ROOT / "internal/docs/openapi.yaml").write_text(json.dumps(spec, indent=2) + "\n")
print(f"Wrote {sum(len(v) for v in paths.values())} operations and {len(schemas)} schemas")
