# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A [libdns](https://github.com/libdns/libdns) provider for Oracle Cloud Infrastructure (OCI) DNS. Single-package Go module (`package oraclecloud`) that implements `GetRecords`, `AppendRecords`, `SetRecords`, `DeleteRecords`, and `ListZones`.

Module path: `github.com/libdns/oraclecloud`

## Build & Test Commands

```bash
go build ./...          # Build
go test ./...           # Run all tests
go test -run TestName   # Run a single test
go mod tidy             # Tidy module graph (CI will fail if this produces changes)
```

## Architecture

All code lives in two source files:

- **`provider.go`** — the entire implementation: `Provider` struct, all five libdns interface methods, OCI client initialisation, authentication strategies, zone resolution, record conversion, and helper functions.
- **`provider_test.go`** + **`test_helpers_test.go`** — unit tests using a `fakeDNSClient` that implements the `dnsAPI` interface in-memory; no live OCI calls needed.

### Key Design Patterns

- **`dnsAPI` interface** (bottom of `provider.go`) — abstracts the OCI DNS SDK client behind five methods (`GetZone`, `GetZoneRecords`, `PatchZoneRecords`, `UpdateRRSet`, `ListZones`). The real client is `sdkDNSClient`; tests inject `fakeDNSClient`.
- **Authentication cascade** — `configurationProvider()` resolves auth via: explicit API-key fields → OCI config file → OCI CLI environment variables → instance principal. The `Auth` field (`auto`, `api_key`, `config_file`, `environment`) controls which path is used.
- **Zone resolution** — `resolveZone()` accepts either a zone name or an OCID, calls `GetZone` to normalise it, and returns a `zoneRef` (canonical name + API reference).
- **Scope/ViewID propagation** — every OCI API request passes through an `apply*Options` helper that sets scope (`GLOBAL`/`PRIVATE`) and optional `ViewID`.
- **Record conversion** — `toLibdnsRecord()` converts OCI `Record` → typed libdns records (e.g. `libdns.Address`, `libdns.CNAME`); `recordToDetails()`/`recordToOperation()` convert the other direction using `libdns.RR.RR()` for serialisation.
- **`SetRecords` is per-RRSet** — records are grouped by `(domain, rtype)` via `groupRecordsByRRSet()`, then each group is atomically replaced with `UpdateRRSet`.

## CI

GitHub Actions runs `go mod tidy` (must be clean), `go build ./...`, and `go test ./...` on Go 1.24 + stable. Releases are automated via `release-please` with conventional commits.

## Conventions

- Use conventional commits (`feat:`, `fix:`, `chore:`, etc.) — release-please generates releases from these.
- OCI CLI environment variable names are defined as constants (`envCLI*`) at the top of `provider.go`.
- Zone names are always normalised to trailing-dot form internally via `normalizeZoneName()`.
- libdns record names use relative form (`@` for apex, `www` for subdomains); OCI uses absolute FQDNs — conversion happens in `absoluteDomainForAPI()` and `toLibdnsRecord()`.
