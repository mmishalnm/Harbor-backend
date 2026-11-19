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

The Flutter app requires backend 1.4.0 for customer selection and direct order assignment. Rebuild and restart the backend after replacing source; rebuilding alone does not update an already running process. When starting from the parent folder, pass the configuration path explicitly: `./harbor-backend/harbor -env-file harbor-backend/.env`. Keep the actual `.env` private and out of source archives.


## Customer selection and delivery assignment (v1.4.0)

Delivery partners can call `GET /api/v1/customers/options?page=1&limit=20` before their first order or trip. It returns active customers in their own business as `{customerId, name}` only, ordered by name and customer ID. Pagination is required; limit is capped at 200. Customer contacts, global ledgers and customer creation remain admin/manager operations. Warehouse staff retain their existing assigned-data access.

Admins/managers assign an existing open order with `POST /api/v1/orders/{id}/assign`:

```json
{"deliveryPartnerId":"USR-000003","vehicleId":"VEH-000001"}
```

`vehicleId` is optional. The transaction reuses an eligible current-window trip for that partner (started first); an explicit vehicle restricts selection to that van. Otherwise it creates today's planned trip with the selected/default vehicle and configured business duration, even when automatic rider-order assignment is off. The response is the updated order with `status: "assigned"` and `tripId`. The selected rider then sees the order on their own trip. Assignment does not move stock or bypass pickup. Already assigned or picked orders cannot be reassigned. Missing vehicle, busy/nonempty van and trip capacity errors roll back the assignment. Concurrent attempts can add the order only once.

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
