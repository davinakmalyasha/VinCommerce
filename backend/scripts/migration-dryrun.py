"""Apply every migration to a scratch database, with goose's own statement splitting.

WHY THIS EXISTS
---------------
`scripts/check-migration.mjs` checks that a migration file is SHAPED correctly: it has a
Down, its indexes are dropped, its constraints are reversed. It cannot tell you the SQL
executes. Nothing executed 00032 -> 00051 until this script existed, and executing them
found six classes of defect that reading them and three static checkers all missed:

  * 21 dollar-quoted blocks with no StatementBegin/End, which goose cuts in half.
  * 4 COMMENT lines ending in `;`, which goose reads as a statement terminator -- so
    `CREATE TABLE ledger_accounts` was truncated mid-body after a CHECK clause.
  * `RAISE EXCEPTION 'a' || 'b'` in 00050. RAISE takes a FORMAT, not a concatenation;
    verified against PostgreSQL rather than assumed.
  * `DROP CONSTRAINT` with no `IF EXISTS` on a constraint that migration itself creates,
    so 00050 could only ever apply to a database it had already half-migrated.
  * `ALTER TABLE ... DROP CONSTRAINT` selected by `LIKE '%status%'`, which matches both
    `orders_status_check` and `orders_payment_status_check`.
  * 00041 adding CHECK constraints BEFORE the repair UPDATEs that were meant to make
    them satisfiable -- the exact inversion 00040's own header warns about.

HOW IT RUNS SQL
---------------
`postgres --single`, one backend in-process, because the normal server cannot fork a
backend on some Windows hosts:

    could not reserve shared memory region (addr=...) for child ...: error code 487

That is session-level shared-memory exhaustion and clears only on logoff; a brand-new
cluster fails identically, so it is not this cluster's data directory. If you can run a
normal server, prefer `goose -dir internal/db/migrations postgres://... up`.

TWO THINGS THIS SCRIPT GETS WRONG ON PURPOSE, BOTH MEASURED

1. Statements are FLATTENED to one line, because single-user mode executes a LINE. Fed a
   multi-line statement it returns no error and creates NOTHING -- verified by counting
   rows in pg_class. A harness that silently executes nothing is worse than none, so
   `run()` asserts the resulting schema actually has tables before reporting a pass.

2. Statements are split with GOOSE'S rule, not a real parser, so that a migration the
   runner would break is reported as broken rather than quietly accepted.

Set PG_BIN and PG_DATA below, or run with the defaults for a Laragon install.
"""

import glob
import io
import os
import re
import subprocess
import sys

PG_BIN = os.environ.get("PG_BIN", r"D:\laragon\bin\postgresql\pgsql-18.6\bin")
DATA = os.environ.get("PG_DATA", r"D:\laragon\data\postgresql\scratch_cluster")
DB = "migration_dryrun"

BEGIN = "-- +goose StatementBegin"
END = "-- +goose StatementEnd"


def split_like_goose(text: str):
    """Reproduce goose v3.x's statement splitter."""
    stmts, buf, state = [], [], "up"
    for line in text.split("\n"):
        st = line.strip()
        if st == BEGIN:
            state = "begin"
            continue
        if st == END:
            if buf:
                stmts.append("\n".join(buf))
            buf, state = [], "up"
            continue
        if st.startswith("-- +goose"):
            continue
        if not buf and (st.startswith("--") or st == ""):
            continue
        buf.append(line)
        if state == "up" and line.rstrip().endswith(";"):
            stmts.append("\n".join(buf))
            buf = []
    if buf and "".join(buf).strip():
        stmts.append("\n".join(buf))
    return stmts


def up_section(sql: str) -> str:
    """Keep only the Up half, but KEEP the StatementBegin/End markers.

    Stripping them here was a bug in this harness: `split_like_goose` honours them, so
    removing them first made every correctly-wrapped dollar-quoted block get split
    anyway -- reproducing a defect that does not exist in 00003 and 00012, and
    reporting it as one. A validator that manufactures false findings is the same
    failure class as one that reports false passes.
    """
    if "-- +goose Down" in sql:
        sql = sql.split("-- +goose Down")[0]
    return sql


def _newline_join(stmt: str, i: int) -> int:
    """Emit the separator for a newline that is about to be collapsed.

    PostgreSQL CONCATENATES string literals that are separated by a newline:

        RAISE EXCEPTION
            'a journal with no entries '
            'would sum to zero and look posted while moving no money';

    That is how a long message is written across lines. Replacing the newline with a
    space instead produces `'a ' 'b'`, which is a syntax error, and it is how every
    multi-line RAISE EXCEPTION in 00043 failed here.

    So when the next non-blank character after the newline is a quote, the separator is
    NOTHING -- which is what PostgreSQL itself does. Otherwise it is one space.
    """
    j = i
    while j < len(stmt) and stmt[j] in " \t\r\n":
        j += 1
    if j < len(stmt) and stmt[j] == "'":
        return j
    return -1  # caller emits a single space


def strip_comments_and_flatten(stmt: str) -> str:
    """Reduce a statement to one line, removing `--` comments outside literals.

    Single-user mode executes a LINE, not a multi-line statement: feeding it
    `CREATE TABLE t (\\n a int\\n);` returns no error and creates NOTHING, which is
    verified by counting rows in pg_class afterwards. A validator built on that
    behaviour without flattening would report success while executing nothing --
    a silent pass, which is the worst possible failure for this tool.

    Quoting is tracked so that a `--` inside a string literal ('--', or a note in
    an RAISE EXCEPTION message) survives, and so that dollar-quoted bodies are left
    exactly as written.
    """
    out = []
    in_s = False      # inside a single-quoted literal
    in_d = False      # inside a double-quoted identifier
    in_dollar = False # inside $tag$ ... $tag$
    dollar_tag = ""
    i = 0
    n = len(stmt)
    while i < n:
        ch = stmt[i]

        if in_dollar:
            # Comments are STILL stripped inside a dollar-quoted body, with string
            # literals still honoured.
            #
            # This was a bug in the harness. Flattening a `DO $$ ... $$` block onto one
            # line means a `--` comment inside the body now comments out the rest of the
            # LINE -- which after flattening is the entire statement. Every plpgsql block
            # in 00040 failed with "syntax error at end of input" for exactly this
            # reason, and the migrations themselves were fine.
            #
            # Stripping a comment inside a dollar body cannot change semantics: within
            # PL/pgSQL a `--` line is a comment, and a `--` that is really part of a
            # string is protected by the quote tracking below.
            if in_s:
                out.append(ch)
                if ch == "'":
                    if i + 1 < n and stmt[i + 1] == "'":
                        out.append("'")
                        i += 2
                        continue
                    in_s = False
                i += 1
                continue
            if ch == "'":
                in_s = True
                out.append(ch)
                i += 1
                continue
            if stmt.startswith("--", i):
                j = stmt.find("\n", i)
                i = n if j < 0 else j
                out.append(" ")
                continue
            if stmt.startswith(dollar_tag, i):
                in_dollar = False
                out.append(dollar_tag)
                i += len(dollar_tag)
                continue
            if ch == "\n":
                j = _newline_join(stmt, i)
                if j < 0:
                    out.append(" ")
                    i += 1
                else:
                    i = j
                continue
            out.append(ch)
            i += 1
            continue

        if in_s:
            out.append(ch)
            if ch == "'":
                # '' is an escaped quote inside the literal
                if i + 1 < n and stmt[i + 1] == "'":
                    out.append("'")
                    i += 2
                    continue
                in_s = False
            i += 1
            continue

        if in_d:
            out.append(ch)
            if ch == '"':
                in_d = False
            i += 1
            continue

        # not in any literal
        if stmt.startswith("--", i):
            j = stmt.find("\n", i)
            i = n if j < 0 else j          # drop the comment, keep the newline as a space
            out.append(" ")
            continue
        if ch == "'":
            in_s = True
            out.append(ch)
            i += 1
            continue
        if ch == '"':
            in_d = True
            out.append(ch)
            i += 1
            continue
        if ch == "$":
            m = re.match(r"\$[A-Za-z_][A-Za-z_0-9]*\$|\$\$", stmt[i:])
            if m:
                dollar_tag = m.group(0)
                in_dollar = True
                out.append(dollar_tag)
                i += len(dollar_tag)
                continue
        if ch == "\n":
            j = _newline_join(stmt, i)
            if j < 0:
                out.append(" ")
                i += 1
            else:
                i = j
            continue
        out.append(ch)
        i += 1

    flat = "".join(out)
    return re.sub(r"\s+", " ", flat).strip()


def run(sql_text: str, db: str = None):
    proc = subprocess.run(
        [os.path.join(PG_BIN, "postgres.exe"), "--single", "-D", DATA, db or DB],
        input=sql_text, capture_output=True, text=True,
        # MUST be explicit. `text=True` alone encodes stdin with the LOCALE encoding,
        # which on this machine is cp1252 -- so an em-dash (U+2014) in a migration
        # comment or an RAISE EXCEPTION message is sent as the single byte 0x97 and
        # PostgreSQL rejects the statement with
        #
        #     ERROR: invalid byte sequence for encoding "UTF8": 0x97
        #
        # which reads exactly like corruption in the repository when the repository is
        # clean. A validation harness must never let the platform's default encoding
        # into the thing it is validating.
        encoding="utf-8", errors="replace",
    )
    return proc.stdout + proc.stderr


def run_db(db: str, sql_text: str):
    return run(sql_text, db)


def main():
    files = sorted(glob.glob("internal/db/migrations/*.sql"))
    # Reset the database so a re-run is meaningful.
    #
    # CREATE DATABASE has to be issued against a database that EXISTS -- it cannot run
    # inside `migration_dryrun`, because that is the database being created. It also
    # cannot run inside a transaction block, which is why single-user mode is the right
    # tool here rather than a complication.
    boot = run_db("postgres", "DROP DATABASE IF EXISTS %s;\n" % DB)
    boot += run_db("postgres", "CREATE DATABASE %s;\n" % DB)
    if "CREATE DATABASE" in boot and "already exists" in boot:
        print("FATAL: could not create %s:\n%s" % (DB, boot[-400:]))
        sys.exit(2)

    failures, applied = [], 0
    for path in files:
        name = os.path.basename(path)
        sql = io.open(path, encoding="utf-8", newline="").read()
        stmts = split_like_goose(up_section(sql))
        for st in stmts:
            if not st.strip():
                continue
            out = run(strip_comments_and_flatten(st) + "\n")
            if re.search(r"\b(FATAL|ERROR|PANIC)\b", out):
                # The backend prints "2026-10-04 21:47:51.933 +07 [5852] ERROR: ...".
                # A `startswith("ERROR")` filter therefore matches nothing and the
                # failure prints with an EMPTY error, which is the least useful
                # possible diagnostic. Match the label anywhere in the line.
                first = ""
                for line in out.split("\n"):
                    if re.search(r"\b(ERROR|FATAL|PANIC):", line):
                        first = re.sub(r"^.*?\b(ERROR|FATAL|PANIC):", r"\1:", line)
                        break
                failures.append({
                    "file": name,
                    "error": first[:200],
                    "stmt": st.strip()[:400],
                    "n": len(stmts),
                })
                break
        else:
            applied += 1
        if failures and failures[-1]["file"] == name:
            continue

    # Prove the run was real: the final schema must actually contain the tables the
    # migrations create. A tool that can silently execute nothing must not be allowed
    # to report a pass.
    probe = run("SELECT count(*) FROM information_schema.tables WHERE table_schema='public';")
    got = [l for l in probe.split("\n") if "count =" in l]
    print("public tables in %s: %s" % (DB, got[0].strip() if got else "UNKNOWN"))
    if not got or 'count = "0"' in got[0]:
        print("FATAL: the dry run executed nothing -- it is not evidence")
        sys.exit(2)

    print("applied cleanly: %d / %d" % (applied, len(files)))
    if failures:
        print("\nFAILURES (%d):" % len(failures))
        for f in failures:
            print("\n--- %s (statement %d of %d)" % (f["file"], f["n"], len(split_like_goose(up_section(io.open('internal/db/migrations/' + f['file'], encoding='utf-8', newline='').read())))))
            print("    error: %s" % f["error"])
            print("    stmt : %s" % f["stmt"].replace("\n", " | "))
        sys.exit(1)
    print("\nALL MIGRATIONS APPLIED CLEANLY")


if __name__ == "__main__":
    main()