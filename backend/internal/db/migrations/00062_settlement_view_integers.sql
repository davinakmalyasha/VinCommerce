-- +goose Up
-- Settlement amounts without float casts.
--
-- The settlement_matches view cast NUMERIC columns to float8 for Go scanners
-- that wanted float64. The scanners take integers now, and the casts would
-- turn exact ledger sums into approximate ones at the reconciliation boundary
-- -- precisely where approximation is unacceptable.
CREATE OR REPLACE VIEW settlement_matches AS
SELECT
    s.id,
    s.gateway,
    s.settlement_ref,
    s.settled_on,
    s.kind,
    s.gross                AS gross,
    s.fee                  AS fee,
    s.net                  AS net,
    s.journal_id,
    s.reconciled_at,
    j.tx_type,
    j.effective_at,
    COALESCE(journal_total.total, 0) AS journal_total,
    -- Signed: positive means the gateway reported more money than our journal
    -- recorded, negative means the reverse. Both are wrong; the sign says which
    -- way to look.
    (s.net - COALESCE(journal_total.total, 0)) AS amount_delta,
    (s.journal_id IS NOT NULL)                 AS is_matched
  FROM gateway_settlements s
  LEFT JOIN ledger_journals j ON j.id = s.journal_id
  LEFT JOIN (
      SELECT journal_id, SUM(amount) AS total
        FROM ledger_entries
       GROUP BY journal_id
  ) journal_total ON journal_total.journal_id = j.id;

-- +goose Down
CREATE OR REPLACE VIEW settlement_matches AS
SELECT
    s.id,
    s.gateway,
    s.settlement_ref,
    s.settled_on,
    s.kind,
    s.gross::float8        AS gross,
    s.fee::float8          AS fee,
    s.net::float8          AS net,
    s.journal_id,
    s.reconciled_at,
    j.tx_type,
    j.effective_at,
    COALESCE(journal_total.total, 0)::float8 AS journal_total,
    (s.net - COALESCE(journal_total.total, 0))::float8 AS amount_delta,
    (s.journal_id IS NOT NULL)                        AS is_matched
  FROM gateway_settlements s
  LEFT JOIN ledger_journals j ON j.id = s.journal_id
  LEFT JOIN (
      SELECT journal_id, SUM(amount)::float8 AS total
        FROM ledger_entries
       GROUP BY journal_id
  ) journal_total ON journal_total.journal_id = j.id;
