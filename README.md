# VictoriaDB

A single-binary Backend-as-a-Service engine built with Go + SQLite + SolidJS.

## Features

- **Dynamic REST API** — full CRUD for any table, auto-generated at runtime
- **Real-time SSE** — subscribe to record changes with `EventSource`
- **Interactive ERD** — drag-and-drop schema editor with FK visualization
- **Data Explorer** — spreadsheet view with inline editing and pagination
- **Code Generator** — JS, Dart/Flutter, cURL, Python snippets per collection
- **Schema Migrations** — add/rename/drop columns without data loss
- **Single binary** — frontend embedded via `go:embed`; deploy one `.exe`

## Quick Start

### Prerequisites
- Go 1.22+
- Node.js 18+

### Build

```powershell
# 1. Install frontend dependencies and build
cd ui
npm install
npm run build
cd ..

# 2. Build the Go binary (embeds the frontend)
& "C:\Program Files\Go\bin\go.exe" build -o victoriadb.exe .

# 3. Run
.\victoriadb.exe --port 8090
```

Open **http://localhost:8090** in your browser.

### Development mode

Run the backend and Vite dev server simultaneously:

```powershell
# Terminal 1 — Go backend (API)
& "C:\Program Files\Go\bin\go.exe" run . --port 8090

# Terminal 2 — Vite frontend (proxies /api to :8090)
cd ui
npm run dev   # http://localhost:3000
```

## REST API

Base URL: `http://localhost:8090/api/v1`

### Schema

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/schema` | List all tables and columns |
| `POST` | `/schema/tables` | Create a new table |
| `PUT` | `/schema/tables/:table` | Add / rename / drop columns |
| `DELETE` | `/schema/tables/:table` | Drop a table |

**Create table example:**
```json
POST /api/v1/schema/tables
{
  "name": "posts",
  "columns": [
    { "name": "title", "type": "TEXT", "notNull": true },
    { "name": "body",  "type": "TEXT" },
    { "name": "author_id", "type": "INTEGER" }
  ],
  "foreignKeys": [
    { "from": "author_id", "toTable": "users", "toColumn": "id" }
  ]
}
```
> An `id INTEGER PRIMARY KEY` column is automatically added when no PK is provided.

### Collections (CRUD)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/collections/:table/records` | List (paginated, sortable, filterable) |
| `POST` | `/collections/:table/records` | Create record |
| `GET` | `/collections/:table/records/:id` | Get by ID |
| `PUT` | `/collections/:table/records/:id` | Update record |
| `DELETE` | `/collections/:table/records/:id` | Delete record |

**Query params for list:**
- `page=1` `perPage=30` — pagination
- `sort=name` or `sort=-name` — ascending / descending
- `filter=field:value` — exact match
- `filter=field:~value` — LIKE search
- `filter=field:>value` / `filter=field:<value` — comparisons

### Real-Time (SSE)

```js
const sse = new EventSource("http://localhost:8090/api/v1/realtime");
// Optional: ?table=users  to filter by table

sse.addEventListener("record.created", e => console.log(JSON.parse(e.data)));
sse.addEventListener("record.updated", e => console.log(JSON.parse(e.data)));
sse.addEventListener("record.deleted", e => console.log(JSON.parse(e.data)));
sse.addEventListener("schema.changed", e => console.log(JSON.parse(e.data)));
```

## Project Structure

```
neodb/
├── main.go                     # Server entry point, route registration, go:embed
├── go.mod
├── pkg/
│   ├── core/functional.go      # HOF middleware (logging, recovery, CORS), generics
│   ├── db/manager.go           # SQLite manager, schema introspection, DML/DDL
│   ├── api/
│   │   ├── handlers.go         # REST handlers (schema + CRUD)
│   │   └── sse.go              # SSE hub, event broadcasting
│   └── schema/migration.go     # Schema diff + auto-migration
└── ui/                         # SolidJS frontend
    ├── src/
    │   ├── App.tsx
    │   ├── stores/db.ts         # Reactive state + API calls + SSE
    │   ├── types/index.ts
    │   └── components/
    │       ├── ERD/Canvas.tsx           # Interactive ERD editor
    │       ├── DataExplorer/SpreadsheetView.tsx
    │       ├── API/Snippets.tsx         # Code generator
    │       ├── Realtime/RealtimeLog.tsx # Live event stream
    │       └── Layout/Sidebar.tsx, Header.tsx
    └── dist/                   # Built output (embedded into binary)
```

## SQLite PRAGMAs

VictoriaDB applies these settings on every connection:

| PRAGMA | Value | Reason |
|--------|-------|--------|
| `journal_mode` | `WAL` | Concurrent reads + writes |
| `foreign_keys` | `ON` | Enforce FK constraints |
| `busy_timeout` | `5000` | Retry on lock instead of failing |
| `synchronous` | `NORMAL` | Balance safety and speed |
| `cache_size` | `-64000` | 64MB page cache |
| `temp_store` | `MEMORY` | Temp tables in RAM |

## Configuration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8090` | HTTP listen port |
| `--data` | `./victoria_data` | SQLite data directory |
