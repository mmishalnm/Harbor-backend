# Harbor-backend

## Display names for mobile screens

`POST /api/v1/display-names` resolves up to 200 explicit business IDs to readable labels. It is a read-only batch lookup, not a directory: inaccessible or missing IDs are omitted. All four roles may call it, with account, location, vehicle, order, trip and invoice restrictions preserved. Riders can resolve customer names across their own business for order selection. Staff customer names and all team names remain limited to visible orders/trips and their own identity. No contacts, balances, credentials or stock quantities are returned. Archived accessible names remain available for audit history. Inputs and expected responses are documented in Swagger.

```json
{"ids":["MVN-000001","LOC-000001","CUS-000001"]}
```

Expected for an authorized user:

```json
{"names":{"MVN-000001":"ICE · 5kg","LOC-000001":"Main Freezer · Kahakootam","CUS-000001":"Sunrise Stores"}}
```

The Flutter app requires backend 1.5.0 for reservations, rider trip planning and bulk actions. Rebuild and restart the backend after replacing source; rebuilding alone does not update an already running process. When starting from the parent folder, pass the configuration path explicitly: `./harbor-backend/harbor -env-file harbor-backend/.env`. Keep the actual `.env` private and out of source archives.


## Customer selection and delivery assignment (v1.4.0)

Delivery partners can call `GET /api/v1/customers/options?page=1&limit=20` before their first order or trip. It returns active customers in their own business as `{customerId, name}` only, ordered by name and customer ID. Pagination is required; limit is capped at 200. Customer contacts, global ledgers and customer creation remain admin/manager operations. Warehouse staff retain trip-scoped access; storage locations are unrestricted when no locations are assigned and restricted when the list is nonempty.

Admins/managers assign an existing open order with `POST /api/v1/orders/{id}/assign`:

```json
{"deliveryPartnerId":"USR-000003","vehicleId":"VEH-000001"}
```

`vehicleId` is optional. The transaction reuses an eligible current-window trip for that partner (started first); an explicit vehicle restricts selection to that van. Otherwise it creates today's planned trip with the selected/default vehicle and configured business duration, even when automatic rider-order assignment is off. The response is the updated order with `status: "assigned"` and `tripId`. The selected rider then sees the order on their own trip. Assignment does not move stock or bypass pickup. Already assigned or picked orders cannot be reassigned. Missing vehicle and trip capacity errors roll back assignment. Planning may share a busy van; exclusive use is claimed at pickup. Concurrent attempts can add the order only once.

In Flutter: **Orders → open order → Assign delivery partner**. New rider orders use the searchable **Customer** selector.

## Run without Docker

MongoDB must support transactions (Atlas or a replica set). Configure the private `.env` using `.env.example`, including `MONGO_URI`, `MONGO_DATABASE`, `JWT_SECRET` and `ADMIN_PASSWORD`. Production is the default application mode; no credential or connection URI is compiled into the source.

```sh
cd harbor-backend
go build -o harbor .
./harbor -env-file .env
```

Swagger loads at `/docs`. Redeploy/restart the backend after a source update; a running old binary does not acquire new routes. For Render, use build command `go build -o harbor .` and start command `./harbor`; configure secrets in the service environment.

Validation:

```sh
gofmt -w internal/store/order_assignment.go internal/store/order_assignment_test.go internal/store/order_settings.go internal/store/display_names.go internal/api/server.go internal/api/order_assignment_integration_test.go internal/api/display_names_integration_test.go
go vet ./...
go test ./...
# To include transaction, assignment, concurrency and acceptance tests:
HARBOR_TEST_MONGO_URI='mongodb://127.0.0.1:27017/?replicaSet=rs0' go test ./... -count=1
```

All new request schemas, expected successful responses, error examples and role declarations are included in `internal/docs/openapi.yaml`. Regenerate using `python3 scripts/generate_openapi.py`.


## Pickup location defaults (v1.4.1)

For warehouse staff and delivery partners, **no assigned locations means pickup from any active storage location in their own business**. Assign one or more locations to restrict pickup to that list. Admin/manager override is unchanged. Use `PUT /api/v1/users/{id}/locations` with `{"locationIds":[]}` to clear a restriction; an omitted/null array remains invalid. Changes take effect with the existing JWT because each pickup transaction reloads the user.

Warehouse staff with no location restriction can browse all business storage locations, warehouses and storage stock, so the mobile pickup selector works before any location assignment. Vehicle stock remains accessible through assigned trips. Staff still need trip assignment, and delivery partners still act only on their own trips. Location and warehouse activity checks, account isolation, insufficient-stock guards and append-only movements continue to apply. The same source-location rule applies to existing warehouse staff receipt/transfer permissions.

The Flutter account and assignment screens explain that leaving all locations unchecked allows pickup throughout the business. Redeploy/restart the backend to apply this behavior; existing app builds can use the changed pickup rule.


## Trip planning, bulk orders and reservations (v1.5.0)

Delivery partners can create their own trips with `POST /api/v1/trips`. They can plan multiple named trips on the same Indian calendar day, using their assigned/default or previously allocated vehicles. They cannot impersonate another partner, select another partner's orders, or assign warehouse staff. A name such as **Morning run** / **Afternoon run** makes runs easy to distinguish; omitted names receive distinct Trip numbers. Admin/manager retain full business trip planning.

Multiple planned trips may share a vehicle. Planning leaves `vehicleReserved` false; the first successful pickup claims the van exclusively, within the stock transaction. Another trip cannot load that van until the loaded trip has delivered/returned all goods and completed. Existing unused planned claims from older releases are safely released during a new claim; loaded/started trips remain protected. Planned trips expose no other trip's vehicle stock. No index is dropped or weakened.

Use optional `tripId` in `POST /api/v1/orders` to choose an explicit trip. Otherwise the existing automatic assignment policy applies. Admin order assignment also accepts `tripId`.

Bulk endpoints accept 1–200 unique order IDs and are **all-or-nothing**:

- `POST /api/v1/orders/bulk-assign`: `{ "orderIds": ["ORD-000001", "ORD-000002"], "tripId": "TRP-000001" }`. Admin/manager may alternatively choose deliveryPartnerId/vehicleId to reuse/create a trip. Riders must choose their own trip and can assign only orders they created. Only open, unassigned, unpicked orders are eligible.
- `POST /api/v1/orders/bulk-cancel`: `{ "orderIds": ["ORD-000001"], "reason": "Customer cancelled" }`. Admin/manager only, open/unassigned/unpicked orders only. Keeps the orders with cancellation actor/time/reason and releases their reservations. No physical stock movement occurs.

### Available stock versus physical stock

An order of **13** against **225** units gives **225 on hand, 13 reserved, 212 available**. Reservations derive from active open/assigned order lines minus picked units; no editable reservation counter or migration is needed. Existing orders are included automatically. Reservations apply to the business storage pool until pickup; per-location displays deterministically distribute that reserved amount across storage locations. Picked vehicle stock is excluded from the storage availability total.

Earlier orders reserve first, ordered by creation time then business order ID. Pickup cannot consume another earlier order's reserved quantity. It consumes the current order's reservation and moves physical stock to its vehicle in the same transaction. Delivery then moves vehicle stock out as before. All physical stock mutations share the variant lock with order creation and pickup. Short stock does not prevent taking an order: the unreserved shortfall appears as **awaiting stock**, is automatically reserved when stock arrives, and cannot be picked until covered. Inventory correction/receipt/return changes recompute availability. Cancellation or an unpicked failed stop releases demand.

`GET /api/v1/inventory/availability?page=1&limit=20` is admin/manager only. It returns paginated active-variant rows plus business-wide totals for onHandQuantity, reservedQuantity, availableQuantity and awaitingStockQuantity in one MongoDB snapshot. Totals cover all variants, not only the page. `GET /api/v1/stocks` keeps `quantity` as physical stock and adds computed `reservedQuantity` / `availableQuantity` for scoped storage rows. Order reads and creation include `reservedItems` / `awaitingStockItems`; picked units are not reserved twice. Staff storage visibility and partner trip ownership continue to apply.

Flutter: **Trips → Add trip**, **Create order → Trip**, and **Orders → Select orders → Assign to trip / Cancel selected**. Stock and overview screens refresh after committed changes. The app retains the configured Render base URL. Deploy backend v1.5.0 and rebuild/install Flutter v1.1.0 together.

Verification covers the 225/13/212 case, FIFO pickup, shortages/receipts, cancellation audit, atomic batch rollback, cross-business isolation, concurrent reservations, exclusive concurrent van claims and sequential same-day runs. Existing delivery/invoice/payment/settlement acceptance tests continue to run against an isolated replica set.


## Partner collections, held cash and salary settlement (v1.6.0)

**Finance → Partner cash** lists each delivery partner's total collections, cash still held, cash handed over and salary adjustments. The ledger includes collection/settlement history, notes, references, actor and date. Partners can read only their own cash ledger; staff have no access. Archived partners remain available for settling outstanding obligations.

Balances are computed from immutable payments, actual trip handovers and append-only `partnerCashEntries`; there is no editable holding balance. Collections on the partner's assigned trips include cash/UPI/bank, but only cash enters the holding. Cash entered by admin for that trip is attributed consistently with existing trip settlement. Digital payments never become held cash.

Admin/manager can record **Cash handover** or **Salary adjustment**, with an amount, mandatory note and optional reference/period. Salary adjustment represents cash retained against wages rather than returned cash. Optional `salaryDue` produces `salaryPayable = salaryDue - amount`, so a ₹3,500 adjustment against ₹10,000 salary records ₹6,500 payable. This is a settlement record and preview, not an editable payroll balance. Customer invoices, payments and oldest-invoice allocations are untouched.

- `GET /api/v1/partners/cash?page=1&limit=20`: paginated partner summaries, admin/manager.
- `GET /api/v1/partners/{id}/cash?page=1&limit=20`: paginated ledger with all-history summary, office or own partner.
- `POST /api/v1/partners/{id}/cash-settlements`: `{ "amount": 3500, "method": "salaryAdjustment", "note": "Adjusted against November salary", "reference": "November 2026", "salaryDue": 10000, "requestId": "unique-client-request-0001" }`.
- `GET /api/v1/trips/{id}/cash-summary`: cash collected, prior partner handovers, salary offsets and remaining cash for final trip settlement.

Partner settlement uses `WithTx`, locks the partner shared with collections/trip settlement, caps the amount at computed cash held and allocates it to oldest outstanding trip cash first. A stable requestId makes an exact retry return the existing entry; changed details return IDEMPOTENCY_CONFLICT. Entries use the shared PAY sequence with unique entryId and account/requestId indexes, and cannot be edited or deleted.

Final trip settlement's `cashHandedOver` is **additional** physical cash returned at that moment. Its difference includes prior cash handovers and salary offsets already allocated to the trip. The app displays those amounts so they are not counted twice. Prior shortage flags remain as historical records even after a later partner-level settlement clears the holding. Excess physical handovers are reported separately, preserving the existing discrepancy audit.

Deploy backend **1.6.0** and app **1.2.0+5** together. Go unit/integration tests cover cash/digital separation, salary payable, exact retry/change conflicts, mandatory notes, concurrent over-settlement prevention, archived partners, tenant isolation, unchanged customer balances and final trip settlement after salary offsets.
