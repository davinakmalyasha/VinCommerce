# VinCommerce

An online marketplace where many sellers sell in one place — like Shopee or Tokopedia, built as a portfolio project. Buyers shop from different stores in a single checkout, sellers run their own stores, and admins keep the marketplace safe and fair.

The interface is in Indonesian. Prices are in rupiah.

## Who it is for

- **Buyers** — browse products, chat with sellers, check out once for items from several stores, pay online or cash on delivery, track parcels, return items, and collect loyalty points and vouchers.
- **Sellers** — open a store, get verified, list products (including bulk upload), manage orders and shipments, go live to sell, and get paid out to their wallet.
- **Admins** — verify stores, handle payments and refunds, resolve disputes, answer support tickets, and watch platform sales.

## What you can do with it

**Shopping**
- Search with typo tolerance, filters, flash sales, product bundles, and store vouchers
- Cart shared between guest and logged-in sessions, multi-store checkout with shipping per seller
- Live shopping: watch a seller's stream, chat, tap like, and buy pinned products — replays stay watchable
- Product reviews with photos, seller replies, Q&A with answer alerts, wishlist, price-drop and back-in-stock alerts
- Orders with live status updates, invoice download, reorder, and parcel tracking

**Payments and money**
- Online payment (QRIS, e-wallets, bank transfer, cards) plus manual-transfer claims verified by admins, wallet balance, and cash on delivery
- Buyer money is held safely until the order completes, then split between seller and platform fee
- Refunds go back through the payment provider; every movement is recorded in a ledger that reconciles

**Seller tools**
- Store verification, product management, low-stock alerts, order fulfilment with parcels and return labels
- Live studio: schedule streams, pin products, watch likes grow in real time
- Wallet, payout requests grouped into bank-ready batches, sales analytics

**Help and account**
- Help center with searchable articles and helpful-votes, FAQ, support tickets with photo attachments, response-time promises per priority, and satisfaction ratings
- Order-linked quick fixes on the contact page (track, invoice) so many issues resolve without a ticket
- Notifications grouped by type with per-category on/off, account security (2FA with backup codes, logged-in device list), address book, and cross-device browsing history

## What is not real yet

No demo can do everything. These still need real-world partners, so they are documented rather than faked:

- Courier shipping labels and live rate checks (parcels are recorded manually for now)
- Automatic bank transfers for payouts (an operator runs a bank file today)
- Guest support tickets and deleting your account

The full list lives in [`docs/BACKLOG.md`](docs/BACKLOG.md).

## Try it locally

```powershell
# 1. Start PostgreSQL, Redis, Mailpit
infra/scripts/start-deps.ps1

# 2. Fill the database with demo data (safe to re-run)
cd backend
go run ./cmd/seed

# 3. Start the API, the background worker, and the web app
go run ./cmd/api          # :8080
go run ./cmd/worker       # background jobs
cd ../web && npm run dev  # :5173
```

Open http://localhost:5173

| Role | Email | Password |
|------|-------|----------|
| Buyer | buyer.sample@vincommerce.com | BuyerPass123! |
| Seller | seller.elektro@vincommerce.com | SellerPass123! |
| Admin | admin@vincommerce.com | AdminPass123! |

Try coupon `WELCOME10` (10% off, min Rp50.000). For real online payment, set `MIDTRANS_SERVER_KEY` in the backend `.env` and `VITE_MIDTRANS_CLIENT_KEY` when building the web app — without keys, checkout uses the manual claim flow and an admin verifies it. Full API docs: http://localhost:8080/api-docs

## For developers

| Part | Built with |
|---|---|
| API + worker | Go |
| Database | PostgreSQL, plus Redis for cache and background jobs |
| Web app | React + TypeScript + Vite, Tailwind styling |

- Architecture and the money flow: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- Known gaps, ordered by severity: [`docs/BACKLOG.md`](docs/BACKLOG.md)
- Backup/restore and operations: [`docs/RUNBOOK.md`](docs/RUNBOOK.md)
- One defect investigated end to end: [`docs/POSTMORTEM.md`](docs/POSTMORTEM.md)
- Past claims this file got wrong, corrected: [`docs/CORRECTIONS.md`](docs/CORRECTIONS.md)

Checks run on every push: backend tests plus a guard that proves the refund protections actually catch bugs, web tests and type checks, browser end-to-end tests, backup-restore drills, and secret scanning.
