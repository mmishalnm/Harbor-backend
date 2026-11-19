# Harbor-backend

## Display names for mobile screens

`POST /api/v1/display-names` resolves up to 200 explicit business IDs to readable labels. It is a read-only batch lookup, not a directory: inaccessible or missing IDs are omitted. All four roles may call it, with account, location, vehicle, order, trip and invoice restrictions preserved. Staff/rider customer/team names come only from visible orders/trips and their own identity. No contacts, balances, credentials or stock quantities are returned. Archived accessible names remain available for audit history. Inputs and expected responses are documented in Swagger.

```json
{"ids":["MVN-000001","LOC-000001","CUS-000001"]}
```

Expected for an authorized user:

```json
{"names":{"MVN-000001":"ICE · 5kg","LOC-000001":"Main Freezer · Kahakootam","CUS-000001":"Sunrise Stores"}}
```

The Flutter app requires backend 1.3.0 or later for these labels. Rebuild and restart the backend after replacing source; rebuilding alone does not update an already running process. When starting from the parent folder, pass the configuration path explicitly: `./harbor-backend/harbor -env-file harbor-backend/.env`. Keep the actual `.env` private and out of source archives.
