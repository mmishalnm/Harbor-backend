# Harbor by Code Wayne — backend

Go 1.22+ with MongoDB, bcrypt, HS256 JWTs, and `net/http` method routes. The implementation keeps `config → database → store → api`; HTTP handlers do not import or query MongoDB. Product masters contain identity only. Variant prices apply to all warehouses.

## Build and run without Docker

Run Go commands from `backend`, the directory containing `go.mod` and `main.go`. Go 1.22 or newer is required. The root executable and `cmd/harbor` share the same startup code.

```sh
cd backend
cp .env.example .env
# Edit .env: set MONGO_URI to your MongoDB replica set or Atlas connection,
# choose PORT, and replace JWT_SECRET and ADMIN_PASSWORD.
# Generate a JWT secret with: openssl rand -hex 32
go build -o harbor .
./harbor
```

Plain `go build` also creates the `harbor` executable. The application defaults to production when no environment is supplied. To select development explicitly, use `go run . -app-env development`. Builds do not need a running MongoDB server; MongoDB and the configured credentials are required when starting the executable.

### Host on a Go server

Copy the `backend` project directory to the server, enter that directory, configure `.env`, and build for the server:

```sh
go mod download
go build -trimpath -o harbor .
./harbor -env-file .env
```

Build on the target server, or cross-compile explicitly. A binary built normally on macOS will not run on Linux. From your Mac, these commands create static Linux binaries for the two common server architectures:

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/harbor-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/harbor-linux-arm64 .
```

Use the binary matching the server's architecture, copy the configuration separately, and run it as `./harbor -env-file /path/to/.env`. Swagger assets are embedded, so no separate docs directory is needed on the server. Keep the process running with your host's process manager. The application listens on the configured `PORT`.

Open **http://localhost:8080/docs** if using the example `PORT`. Swagger UI, CSS and the OpenAPI contract are embedded; no CDN is needed. Call `POST /api/v1/auth/login`, then paste `accessToken` into **Authorize**. The bootstrap administrator uses `ADMIN_USERNAME` and `ADMIN_PASSWORD` and belongs only to the initial `Harbor Business` account. It has no access to businesses created by signup. Bootstrap creates that account once; changing the environment password does not reset an existing account.

Set `MONGO_URI` to an existing replica set or Atlas; a standalone MongoDB server is rejected because stock and money operations require transactions. For deployment, supply an authenticated MongoDB replica-set URI and terminate HTTPS at your reverse proxy.

Docker is optional. To use the bundled local development database instead, set `MONGO_PORT` and the matching local URI in `.env`, then run `docker compose up -d --wait`. To build the application container: `docker build -t harbor-backend .`, then supply the environment values to the container.

Configuration precedence is **flags → environment → .env**. `APP_ENV` defaults to `production` when omitted or empty in the environment/configuration file; an explicit `APP_ENV` or `-app-env` overrides it. Mongo URI and HTTP port have no runtime defaults. Supported flags: `-env-file`, `-mongo-uri`, `-mongo-database`, `-port`, `-app-env`. A JWT secret of at least 32 bytes and an administrator password of 12–72 bytes are required in every environment, including production. Never commit `.env`.

```sh
./harbor -env-file .env -port 8090
```

## Independent businesses and admin signup

`POST /api/v1/auth/signup` creates a business and its owner admin together. Capture the owner's name, email, phone and password, the business name/type, business contact details and address. `username` is optional and defaults to email. A successful signup returns a JWT, user and business profile, so the new owner can immediately log in and manage the business.

```json
{
  "name": "Arun",
  "username": "arun",
  "email": "arun@example.com",
  "phone": "9000000001",
  "password": "Replace-with-a-secret",
  "businessName": "Arun Frozen Foods",
  "businessType": "Frozen food distribution",
  "businessEmail": "orders@example.com",
  "businessPhone": "9000000001",
  "address": {
    "line1": "Main Road",
    "line2": "",
    "city": "Thrissur",
    "state": "Kerala",
    "postalCode": "680001",
    "country": "IN"
  }
}
```

Varun signs up separately with his own login and `Varun Stationery`. The two businesses have independent products, prices, customers, users, stock, trips, invoices, payments, balances and settlements. Usernames and supplied user emails are globally unique so staff/riders can log in without choosing a business manually. Business names do not need to be unique.

- `GET /api/v1/auth/me` returns the current user, role, location assignments and own business.
- `GET /api/v1/account` returns the current business profile. The admin can update it with `PUT /api/v1/account`.
- Signup always creates an owner admin for a new business. It does not accept `role`, `accountId` or an existing business to join.
- Only the business admin creates team members through `POST /api/v1/users`, with `manager`, `warehouseStaff` or `deliveryPartner` roles. Team members inherit the admin's account automatically and use `POST /api/v1/auth/login` only.
- Assign storage locations with `PUT /api/v1/users/{id}/locations`. Assign pickup staff through `warehouseStaffIds` on trip creation, or `PUT /api/v1/trips/{id}/staff` while the trip is planned. Staff pickup needs both trip and source location assignment. An empty array clears an assignment.

| Role | Business access |
| --- | --- |
| Admin | Own business data, business profile, team creation and assignments |
| Manager | Operations and assignments within the same business; cannot create users or update the business profile |
| Warehouse staff | Assigned locations/stock and pickup trips; assigned-trip orders and orders they created; business product catalogue |
| Delivery partner / rider | Create orders; see their created orders and own trips, orders/invoices/van stock, and customer dues on their own planned/started trips; business product catalogue |

Every protected database operation requires account context derived from the signed JWT and current user. The database layer ANDs `accountId` into every read/update and stamps it onto every inserted/upserted record. Caller-supplied account IDs cannot replace that constraint. Tokens without account ownership are rejected. Domain IDs are sequences **within each business**; the logical key is `(accountId, businessId)`, so identical `MPN-`/`USR-` IDs in two businesses refer to different records. Invoice numbers and financial-year counters are independent per business as well. Application code never uses MongoDB document identifiers.

### Automatic delivery-partner orders and daily trips

The admin enables the policy with `PUT /api/v1/account/order-settings`:

```json
{
  "autoAssignDeliveryPartnerOrders": true,
  "tripDurationDays": 1
}
```

The default duration is **one Indian calendar day**, midnight to the next midnight in IST. Admins can choose 1–365 days; existing trips keep the duration set when they were created. Automatic assignment is off until the admin enables it. Read the effective settings with `GET /api/v1/account/order-settings`.

Assign each rider a default vehicle through `PUT /api/v1/users/{id}/delivery-vehicle` with `{"vehicleId":"VEH-000001"}`. This determines the vehicle when a trip must be created automatically; it does not reassign an existing trip. An empty `vehicleId` clears the default.

When a rider calls `POST /api/v1/orders`:

1. With automatic assignment enabled, the order is added as a pending stop to their own trip whose scheduled window includes today. An ongoing `started` trip is preferred over a `planned` trip. Ties use the earliest start time, creation time, then trip ID.
2. If no eligible trip exists, a planned trip is created for today using the admin-assigned vehicle and configured duration. Trip creation, order creation and stop assignment commit together in one transaction. Concurrent first orders share that new trip.
3. The response includes `tripId` and `status: "assigned"`. With automatic assignment off, the rider can still create an open order. Riders can list their own created orders and orders on their assigned trips through `GET /api/v1/orders`.
4. Creating or assigning the order never deducts stock. New orders added after departure must be picked through the trip pickup endpoint before delivery. A fully picked order cannot be bypassed by borrowing quantities already loaded for another stop. An unpicked stop can still be closed as failed with a reason.

A new automatic trip requires an assigned, active vehicle which is empty and not reserved by an unfinished trip. Missing vehicle configuration returns `DELIVERY_VEHICLE_NOT_ASSIGNED`; an occupied vehicle returns `VEHICLE_BUSY`; a full current trip returns `TRIP_FULL`. These failures roll back the order as well. Finish the previous trip before reusing its vehicle. The schedule controls automatic assignment and never discards stock, closes stops or settles cash at midnight. Existing trips may still be completed safely after their scheduled window ends.

New trip responses include `tripDate`, `durationDays`, `startsAt` and exclusive `endsAt`. Manual trip creation accepts optional `startDate` (`YYYY-MM-DD`) and otherwise uses today; it also snapshots the admin's duration setting. Older undated trips use a one-day window based on their creation date. The admin can assign warehouse pickup staff to newly created automatic trips as usual, or the rider can pick from storage locations assigned to them.

### Upgrade existing single-business data

An older installation has records without `accountId`. Startup refuses these records instead of exposing them to a signup or guessing ownership. Stop the old application, take a database backup, then run this **once** against its existing `.env`:

```sh
go run ./cmd/migrate-account -env-file .env \
  -owner-username admin -business-name "Your Existing Business"
go build -o harbor .
./harbor
```

Use an existing active admin username as the owner. The migration atomically assigns all previously unowned records to that original business and preserves IDs, stock, invoice numbers, amounts and counters. It then installs account-scoped unique indexes and removes the known obsolete global constraints. Other business-owned records stay with their existing owners. Existing team members remain in that business; assign pickup trips explicitly under the new rules. Log in again after upgrading because old tokens have no account claim. This command is provided for the operator; no migration has been run on your configured remote database.

## Swagger acceptance walkthrough

Use IDs returned by each request. Examples such as `MVN-000001` assume a fresh database. All operations below appear under `/api/v1` in Swagger.

1. Log in as admin. Create `products` with `{"name":"ICE"}`. Create two `variants` referencing that `productId`: `5kg` at `120`, and `2.5kg` at `65`.
2. Create warehouse `Kahakootam`. Create locations `Main Freezer` (`freezer`), `Storeroom Freezer` (`freezer`) and `Storeroom A` (`storeroom`) referencing its `warehouseId`.
3. `POST /stock/receive`: receive 100 units of 5kg into Main Freezer, 25 units of 5kg into Storeroom Freezer, **and 5 units of 2.5kg into Main Freezer**. The last receipt supplies the stock needed for the requested order.
4. Create a `warehouseStaff` user with a 12–72 byte password. `PUT /users/{id}/locations` with `{"locationIds":["<main-freezer-id>"]}`. Create a `deliveryPartner` user as well. Only admin/manager can create customers.
5. Create customer `Sunrise Stores`. Create an order with `source: "phone"` and items of 20 × 5kg plus 5 × 2.5kg. Create a vehicle and then a trip with its `vehicleId`, the `deliveryPartnerId`, and `orderIds: ["<order-id>"]`. Include `warehouseStaffIds: ["<staff-user-id>"]` so the assigned warehouse staff can pick it.
6. Log in as the warehouse staff user and replace the Swagger authorization token. `POST /trips/{id}/pickup` with that `orderId`, Main Freezer's `locationId`, and both order items. An attempt from Storeroom Freezer returns `LOCATION_NOT_ASSIGNED`. Stock transfers and receipts also respect staff assignments. An incomplete pickup prevents starting the trip.
7. Log in as the assigned delivery partner. `POST /trips/{id}/start`. Close the stop with the body below. A second close returns `STOP_ALREADY_CLOSED` and cannot create a duplicate invoice or payment.

```json
{
  "delivered": [
    {"variantId": "MVN-000001", "quantity": 18},
    {"variantId": "MVN-000002", "quantity": 5}
  ],
  "reason": "Two 5kg bags not accepted",
  "collectedAmount": 3500,
  "paymentMethod": "cash"
}
```

8. Inspect the returned invoice and `GET /trips/{id}/stock`. The invoice totals **₹2,485**: `18 × 120 + 5 × 65`. Its number is `INV/26-27/00001` for the first invoice in financial year 2026–27. The van holds 2 × 5kg and 0 × 2.5kg. Delivered quantities have left the van; the two undelivered bags remain there.
9. As admin, inspect `GET /customers/{id}/ledger`. With no previous invoices, the ₹3,500 payment allocates ₹2,485 to this invoice; outstanding is ₹0, advance credit is **₹1,015**, and balance is **−₹1,015**. With older unpaid invoices, those are paid first. The ledger includes paginated invoice/payment entries, and summary totals cover the full ledger.
10. As the assigned partner, `POST /trips/{id}/returns` with Main Freezer's location and 2 × 5kg. Every stop must be closed before leftovers can be returned. `POST /trips/{id}/complete` now succeeds because the van is empty. Main Freezer now holds 82 × 5kg; Storeroom Freezer still holds 25.
11. As admin/manager, `POST /trips/{id}/settle` with `{"cashHandedOver":3450,"note":"50 short"}`. Expected: `cashCollected:3500`, `difference:-50`, `hasDifference:true`. Passing 3500 instead produces zero difference. UPI/bank receipts do not count as cash.

A pickup can create its order: create a trip with `orderIds: []`, then pass `order` instead of `orderId` to `/pickup`. The nested order uses the same schema as `/orders`. Staff and delivery partners need assignment to the source location; admin/manager override it. Use `[]` when replacing assignments to clear them.

## Stock, money, and audit rules

- All mutations run through `WithTx`, with snapshot reads and majority commits. The driver retries transient transaction errors. A duplicate caused by first-use counter/stock-row races retries the entire aborted transaction. Callback state is reset on every retry.
- `move()` is the sole stock writer. It guards debits against insufficient stock and writes a new `MOV-` record atomically with both sides. Receive, transfer and adjustment endpoints accept storage locations only. Vehicle stock changes through trip pickup, delivery and return operations.
- A vehicle is reserved by one planned/started trip. A unique partial index enforces that reservation. Closed trips cannot expose stock belonging to a subsequent trip on the same vehicle.
- Stop closure atomically claims a pending stop inside the transaction. Delivery uses current variant prices, snapshots invoice lines, and leaves undelivered stock on the van. Empty `delivered: []` means a failed delivery; a reason is required for partial/failed stops.
- Customer record revisions serialize billing, cancellation and payment allocation. Money is stored as two-decimal `float64`; calculations use integer cents. Invoice totals and individual payments are limited to ₹1 billion. Quantities are whole units, at most 1 million per item, with at most 200 items/orders per request/trip.
- Invoices, payments and append-only allocation records determine outstanding/advance credit. Customer balance is never an editable field. Existing credit is applied before newer payments; invoices are sorted oldest first. Financial years use Indian Standard Time and roll over at midnight on 1 April.
- Invoice number counters advance in the same transaction as invoice creation. Each business and financial year supports numbers 00001–99999; exhaustion fails without skipping a number. Cancelled invoices retain their numbers. An invoice with any allocated payment/advance cannot be cancelled. Cancellation changes accounting only; physical returns are separate stock operations.
- Product, variant, customer and user archive endpoints retain records. Stock movements, payments, allocations and settlements have no edit/delete endpoints. Variants with stock or open orders must be cleared before archiving. Archived customers can still have existing financial obligations reconciled; new orders require an active customer. Settlements are recorded once, after trip completion.
- Every business collection has a unique compound index on account and its business key. MongoDB document identifiers are never used by the application or exposed in responses. The API serializes explicit typed models, including no password hash.
- Delivery partners can access only their assigned trips, invoices on those trips, and customer ledgers on their own planned/started trips. They cannot create customers. Separate `/payments` collections require their own started trip and a customer on it.
- Every list accepts `page` and `limit` (maximum 200). Ledger summaries use a transaction snapshot. Errors always use `{"error":{"code":"...","message":"..."}}`; unexpected details are logged server-side.

Stop closure and settlement are protected against duplicate submissions. Receipt, pickup, adjustment, standalone payment and return endpoints represent new actions on every successful call; confirm the existing result before repeating an uncertain request. A future idempotency-key contract can extend those operations.

## Checks

```sh
gofmt -w main.go cmd internal
go vet ./...
go test ./...

# Includes real transaction, acceptance, and concurrency tests.
HARBOR_TEST_MONGO_URI='mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true' \
  go test ./... -count=1
```

MongoDB tests create and drop uniquely named `harborTest_*` databases on the supplied replica set. They skip unless `HARBOR_TEST_MONGO_URI` is set. They cover the acceptance flow, rollback after a later pickup item fails, concurrent pickup, concurrent stop closure, current pricing, advances, oldest-first allocation, cancellation and cash-only settlement. Automatic-trip tests cover concurrent first orders, ongoing-trip reuse, pickup checks for late orders, expired windows, default/configured duration and separate-business settings. Account tests additionally cover independent admin signup, stock/pricing/financial isolation, guessed foreign IDs, tampered tenant claims, assigned staff visibility, and legacy ownership migration. Unit tests cover signup validation, fail-closed account scoping, numbering/IST boundaries, allocation rounding, JWT integrity/expiry, configuration and role/contract consistency.

`internal/docs/openapi.yaml` is the embedded OpenAPI 3.0.3 contract, serialized as JSON (valid YAML 1.2). Edit `scripts/generate_openapi.py` and run `make docs` to regenerate it; commit both files. Every operation has request schemas, role declarations, and expected success/error examples. Contract tests check implementation and documentation in both directions. Vendored Swagger UI 5.17.14 retains its license under `internal/docs/ui/LICENSE`.

## Extension points

Keep future GST/IRN processing at `createInvoice`, batch/expiry references at `move`, and barcode inputs at request validation. Trips/stops provide the boundary for later route planning. Notifications belong after a successful commit, using a future transactional outbox; do not send external effects from retryable transaction callbacks. V1 does not implement those features, credit notes or multi-currency.
