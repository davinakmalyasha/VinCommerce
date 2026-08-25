# Security Policy

## Supported versions

Only the latest `main` branch receives security fixes.

## Reporting a vulnerability

**Do NOT open a public GitHub issue for security vulnerabilities.**

This platform handles real money (escrow, wallets, payouts, Midtrans payments), so
please report privately:

1. Email: `security@vincommerce.com` (or the maintainer address in git log)
2. Include: description, affected endpoint/file, reproduction steps, impact assessment
3. You will receive an acknowledgment within 72 hours

We follow coordinated disclosure: please give us up to 90 days before publishing details.

## Security-relevant design notes

- Access tokens are short-lived JWTs kept **in memory only** on the client; refresh
  tokens are httpOnly cookies with rotation + reuse detection (family revocation).
- Payment webhooks are HMAC/SHA512-verified; amounts are re-checked against stored
  intents; status downgrades after capture are rejected.
- The sandbox payment gateway is compiled out of production builds and its webhook
  secret is dev-only. `APP_ENV=production` refuses to boot with sandbox configured.
- All money movements (capture, escrow release, refunds, payouts) run inside single
  database transactions with guarded state transitions.
- Seeding demo data is blocked against production environments (`SEED_FORCE=yes`
  overrides at your own peril).

## Hardening expectations for operators

- Serve TLS at the reverse proxy (HSTS) — the bundled nginx config is HTTP dev-only.
- Set a strong `JWT_SECRET`, change `DB_PASSWORD`, use `DB_SSL_MODE=require`+.
- Restrict Postgres/Redis to private networks; never expose Mailpit publicly.
- Scrape `/metrics` from an authenticated/internal network path only.
- Schedule database backups (pg_dump/PITR) and rehearse restores — none ship in-repo yet.
