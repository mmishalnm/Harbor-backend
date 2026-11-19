# Validation — 19 November 2026

- Go 1.25.2 on macOS ARM64; module minimum Go 1.22.
- MongoDB 8.0.16, isolated single-node replica set.
- `gofmt -w cmd internal`: completed.
- `go vet ./...`: passed.
- `go test ./... -count=1` with `HARBOR_TEST_MONGO_URI`: passed, including all four MongoDB integration tests.
- OpenAPI 3.0.3 validation: passed with Swagger Parser; 388 request/response examples validated against their schemas with AJV.
- Embedded Swagger UI opened successfully at `/docs`, displaying all operations and schemas without external assets.

The MongoDB suite verifies the requested receipt/pickup/delivery/return/settlement flow, rollback of a multi-item pickup, concurrent stop closure, competing pickups, vehicle reuse isolation, payment allocation, advances, current prices, invoice cancellation, archived customer reconciliation, pagination and role restrictions. Test databases are created with unique names and dropped after each test.

The sample delivery produces a ₹2,485 invoice and ₹1,015 advance credit from a ₹3,500 cash collection. A ₹3,450 cash handover records a −₹50 difference.

## Native build entry point update

- Added `main.go` beside `go.mod`; both it and `cmd/harbor` use the same `internal/app` startup function.
- Plain `go build` from `backend`: passed, producing the native macOS ARM64 executable.
- Static Linux AMD64 and ARM64 cross-compilation from the module root: passed; executable architectures checked with `file`.
- Existing `go build ./cmd/harbor` entry point: passed.
- `gofmt`, `go vet ./...`, and `go test ./...`: passed after the change. MongoDB integration tests were not rerun for this startup/build-only change; the initial full integration run above remains recorded separately.

## Default production mode update

- Unspecified application mode and an empty dotenv `APP_ENV` now default to `production`. Explicit flags, environment values and dotenv selections retain their precedence.
- Added configuration tests for the default, overrides, invalid/empty explicit flags and required secrets when relying on the default production mode.
- The startup log includes the selected environment, and the example configuration uses production.
- `gofmt`, `go vet ./...`, `go test ./...` and rebuilding the native executable all passed. MongoDB integration tests were not rerun for this configuration-only change.

## Independent business accounts and admin signup

- Added public owner signup, own-business profile read/update, current-user profile, and pickup staff assignment endpoints. Swagger now documents 57 operations and 69 schemas.
- JWTs include account ownership. Scoped database reads, counts, updates, inserts, upserts and sequence counters fail closed without an account and prevent changing document ownership.
- Business IDs and invoice numbers have account-scoped unique indexes. Staff visibility follows assigned locations and pickup trips; riders retain own-trip restrictions. Only an account admin creates team members.
- All seven MongoDB integration tests passed, including concurrent signup with no duplicate/orphan business, independent Arun/Varun stock/pricing/financial data, guessed foreign IDs, tenant claim tampering, staff assignments, legacy migration and the existing delivery/payment/concurrency scenarios.
- Unit tests passed for signup validation, immutable ownership, required account context, and rejecting old JWTs with no business claim.
- `gofmt`, `go vet ./...`, `go test ./... -count=1` with a disposable MongoDB replica set, and native `go build` passed.
- OpenAPI validation and all 429 request/response examples passed. Admin signup and its business details rendered correctly in the embedded Swagger UI.
- No migration or business data changes were performed on the configured remote database. The documented offline migration command is provided for existing single-business records.

## Automatic rider-order trips and configured duration

- Added admin-controlled automatic order assignment, per-rider default vehicle assignment and business trip duration settings. The default duration is one IST calendar day; new trips snapshot the configured 1–365 day duration.
- Rider orders join their own current-window trip, preferring an ongoing trip. If none exists, order/trip/stop creation commits atomically using the assigned empty, available vehicle. Concurrent first orders share one new trip.
- Late orders can be picked into a started trip; delivery rejects an unpicked order before it can consume another stop's stock. Scheduling never silently closes trips or discards remaining stock/cash.
- All ten MongoDB integration tests passed, including automatic daily creation, concurrent orders, disabled-policy behavior, missing vehicle rollback, rider/business isolation, late pickup validation, expired windows and duration changes without retroactive edits.
- Added unit tests for default/invalid duration, IST midnight and exclusive end boundaries, date validation, legacy trip windows and deterministic ongoing-trip preference.
- `gofmt`, `go vet ./...`, full `go test ./... -count=1` against the disposable replica set, and native `go build` passed.
- OpenAPI 1.2.0 contains 60 operations and 71 schemas; specification validation and all 455 request/response examples passed.
