# AGENTS.md

This file defines concise working rules for contributors and coding agents in this repository's panel initiative.

## 1) Product Scope (MVP)

Build a simple control plane for:

- Socks5 residential endpoint pool management
- Customer and order management
- IP oversell control (concurrent assignment limit)
- Access protocols: VMess, VLESS, SS, Mixed inbound
- One-click TXT import of Socks5 endpoints
- Batch output: links, subscription base64, QR PNG, ZIP bundle
- Xray config publish via Agent gRPC (validate + reload)

Hard decisions for MVP:

- Single-host Docker deployment
- Traefik as ingress in Compose deployment
- pnpm for frontend dependency management/build
- PostgreSQL storage
- RBAC simple roles
- No data isolation between admins

## 2) Architecture Snapshot

- Frontend: Vite + TypeScript + React + Ant Design
- Backend: Go + Gin + Gorm
- Runtime: Xray-core + local Agent (gRPC)
- Data flow:
  1. Panel stores business state in PostgreSQL
  2. Panel builds full Xray JSON from DB state
  3. Panel calls Agent `ApplyConfig`
  4. Agent validates, writes config, reloads Xray

Routing strategy baseline:

- Generate rule per active order using `user/email -> socks outbound tag`
- Keep one deterministic fallback outbound (`direct`)

## 3) Engineering Best Practices

- Keep handlers thin; move logic into service layer.
- Use transactions for assignment/release paths.
- Validate all external input (import text, API payloads).
- Use strict schema constraints (unique/index/not-null).
- Redact sensitive fields from logs and API outputs.
- Add tests for parser, allocator, and config generation.
- Keep generated artifacts reproducible and deterministic.
- Prefer explicit errors over silent fallback.

## 4) Security Baseline

- Hash admin passwords with bcrypt.
- Use JWT with expiry and server-side role checks.
- Keep secrets in environment variables only.
- Treat socks credentials and access links as sensitive.
- Validate Xray config before reload in normal flows.

## 5) Operational Guidelines

- Maintain `last-good-config` for fast rollback.
- Publish operations must produce audit logs.
- Add health checks for backend and agent.
- Keep deployment scripts idempotent.
- Provide/maintain host-level one-click installer for `Xray-core + Agent`.

## 6) Forbidden Actions

Do NOT do the following without explicit approval:

- Do not hardcode passwords, tokens, or private keys.
- Do not bypass RBAC checks in backend endpoints.
- Do not skip oversell checks when activating orders.
- Do not reload Xray with unvalidated config in production paths.
- Do not mutate DB counters outside transactional logic.
- Do not expose raw stack traces or SQL errors to clients.
- Do not commit secret-bearing files (`.env`, key material).
- Do not force-push or rewrite shared history.

## 7) Definition of Ready for New Features

- Requirement is explicit (input, output, edge cases).
- DB impact is defined (migration, indexes, rollback).
- API contract is documented.
- Security impact is reviewed.

## 8) Definition of Done for PRs

- Core acceptance path tested end-to-end.
- Unit/integration tests pass for changed behavior.
- Logs are structured and free of secrets.
- Docs updated when behavior or schema changes.
