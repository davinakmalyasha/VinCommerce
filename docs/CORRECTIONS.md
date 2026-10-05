# Corrections to claims this README previously made

A running log of claims retracted here, kept because a README that
quietly fixes itself teaches a reader nothing about which parts of it
to trust. Several of these were found by running the thing rather than
by reading it.


* **"No SQL in `00043`-`00051` has ever been executed" is no longer true — and while
  it was true, it was hiding six classes of defect.** `backend/scripts/migration-dryrun.py`
  now applies `00001` -> `00051` to a scratch database and all 51 apply cleanly (78
  tables). Executing them found:

  - **21 dollar-quoted blocks with no `-- +goose StatementBegin/End`.** goose v3.x splits
    a migration on `endsWithSemicolon(line)` and has **no dollar-quote awareness**, so
    every `DO $$ ... $$` and `CREATE FUNCTION $$` whose body contains a semicolon was
    being cut in half. `00003` and `00012` had the markers; `00032`-`00051` did not.
  - **4 comment lines ending in `;`** (6 occurrences). goose's predicate cannot see
    comments, so a `--` line ending in a semicolon reads as a statement terminator.
    The worst was `00043:51`, which truncated `CREATE TABLE ledger_accounts` after
    `CHECK (class IN (...)),` -- leaving a trailing comma and no closing paren.
  - **`RAISE EXCEPTION 'a' || 'b'` in `00050`** (mine, from the shipment work). RAISE
    takes a *format*, not a concatenation; verified against PostgreSQL rather than
    assumed. Two blocks.
  - **`ALTER TABLE order_items DROP CONSTRAINT` with no `IF EXISTS`** in `00050`, on a
    constraint `00050` itself creates -- so it could only ever apply to a database that
    had already been half-migrated by itself.
  - **`ALTER TABLE ... DROP CONSTRAINT` selected by `LIKE '%status%'`** in `00050`, which
    matches both `orders_status_check` and `orders_payment_status_check`. `SELECT ...
    INTO` keeps one arbitrary row, so it could drop the payment check and leave the
    status check rejecting `partially_shipped`. Now matched by `conkey` -- the column
    the constraint covers.
  - **`00041` added CHECK constraints BEFORE the repair UPDATEs** meant to make them
    satisfiable, and added `payment_intents_split_sums` and `orders_money_foots` with
    no repair at all. `00041`'s own header says the split holds "AFTER rounding the
    first"; there was no rounding pass. Both are now repair-then-`NOT VALID`-then-
    `VALIDATE`, so the constraint installs without taking ACCESS EXCLUSIVE on tables
    checkout writes on every order.

  Also fixed: `00050`'s Down dropped `order_items_status_check` and never restored it,
  leaving the column unconstrained; its line vocabulary dropped `'packed'`, which
  `00004` allows; its backfill joined every outbound parcel instead of parcel 1 (so a
  re-run gave a new parcel the whole order again) and could abort on a duplicate AWB;
  and `order_items.weight_grams` had no `>= 0` guard, so one negative historical row
  failed the new `shipments` CHECK.

* **The dry-run harness itself had four bugs, each of which produced a FALSE finding.**
  It stripped `StatementBegin/End` before splitting, so it reported 00003 and 00012 as
  broken when they were correct. It flattened statements before stripping `--`
  comments, so a comment inside a `DO $$` body swallowed the rest of the statement. It
  used `subprocess(text=True)`, which encodes with the *locale* encoding -- on Windows
  that is cp1252, so an em-dash became the byte `0x97` and PostgreSQL reported
  `invalid byte sequence for encoding "UTF8"` against a repository that contains no
  invalid UTF-8 at all. And it applied the Down sections in forward order, which
  produced 25 failures that were pure ordering noise.

  Each is recorded here because a validator that invents defects is as useless as one
  that reports false passes, and `00043`/`00050` were committed by me on the strength
  of static checks alone.



* **`2814c52` claimed "39/39 mutations caught". It was 38 of 39.** M35 -- the
  mutation for the `NOT EXISTS` that stops a payout sitting in two remittance files
  -- never ran. Its anchor was one tab out of place, and the harness printed `SKIP`
  and then `continue`d without counting it, so the summary read `38 caught, 0
  missed, 39 total` and exited 0. The arithmetic invites the reader to conclude
  39/39. Fixed in `3b9ce88`; M35's anchor and two false passes in the test that was
  supposed to catch it are fixed here. The claim was uncovered while the report said
  otherwise.

* **Mutations outside a hardcoded file list were never tested at all.** The runner
  chose the package with `if m.file == repoFile`, so any other file fell through to
  `./internal/service`; a mutation that rewrote `shipment_repo.go` ran only tests
  that never read it and was reported `SURVIVED`. Fixed in `d5d3e13`, which runs
  every package -- deriving the package from the path was tried first and is wrong,
  because this project's tests are largely source-text assertions that live in a
  different package from the file they inspect.
