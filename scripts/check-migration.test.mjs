#!/usr/bin/env node
/**
 * Mutation test for scripts/check-migration.mjs.
 *
 * A guard that cannot fail is worse than no guard, because it is read as evidence
 * of something. This reintroduces each of the four defects the guard exists to
 * catch and asserts the guard rejects every one.
 *
 * The defects, for reference:
 *
 *   1. The CI step labelled "Validate the newest migration" passed no filename,
 *      and the script defaulted to 00040. Every migration added since -- including
 *      the double-entry ledger -- was never checked.
 *   2. It required `-- +goose Up` exactly once. Goose treats everything before
 *      `-- +goose Down` as the Up section, and NO migration in this repository
 *      uses the explicit marker, so every real file failed. A check that cannot
 *      pass is not a check.
 *   3. It counted `DO $$` openers against `END $$;` enders, which only understands
 *      anonymous plpgsql blocks. Trigger functions are
 *      `CREATE OR REPLACE FUNCTION ... AS $$ ... END $$;`, so 00043's three
 *      correct function bodies reported "0 open / 3 end" and FAILED. A false
 *      positive on correct code, on the most important structural property there
 *      is.
 *   4. It required `CREATE INDEX IF NOT EXISTS`, so an index created without the
 *      guard clause was invisible to the reversibility check -- "all 0 created
 *      indexes are dropped" reported success on migrations creating six.
 *
 * Plus the one that makes a silent rollback look correct: 00043's Down is
 * `SELECT 1;`, retaining fifteen indexes on purpose. It was indistinguishable
 * from a forgotten rollback, so the guard now requires that choice to be
 * declared.
 *
 * Run: node scripts/check-migration.test.mjs
 */

import { execFileSync } from 'node:child_process'
import { mkdtempSync, writeFileSync, mkdirSync, readFileSync, copyFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..')
const checker = path.join(here, 'check-migration.mjs')
const migDir = path.join(repo, 'backend', 'internal', 'db', 'migrations')

// A minimal but VALID goose migration: a function body (the case the old
// `DO $$` counter got wrong) and an index that Down removes.
const GOOD = `-- 9999_fixtures.sql
CREATE INDEX IF NOT EXISTS idx_fixtures_a ON fixtures (id);
CREATE OR REPLACE FUNCTION fixtures_fn() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    n INT;
BEGIN
    SELECT COUNT(*) INTO n FROM fixtures;
    RETURN NULL;
END $$;

-- +goose Down
DROP INDEX IF EXISTS idx_fixtures_a;
`

const run = (file) => {
  // A file of `undefined` is worse than no argument at all: the checker joins it
  // onto the directory and opens a path literally ending in "undefined", which
  // is a crash rather than a result. This distinction matters here because one of
  // the cases under test IS the no-argument default.
  const args = file === undefined ? [checker] : [checker, file]
  try {
    const out = execFileSync(process.execPath, args, { encoding: 'utf8' })
    return { code: 0, out }
  } catch (e) {
    return { code: e.status ?? 1, out: (e.stdout ?? '') + (e.stderr ?? '') }
  }
}

// The checker resolves the migrations dir relative to its own location, so the
// fixtures are written into the real migrations directory under a name that
// sorts last -- and removed afterwards. A temp copy of the script would resolve a
// different directory and silently test nothing, which is exactly the mistake
// above.
const FIXTURE = '9999_zz_migration_fixtures.sql'
const fixturePath = path.join(migDir, FIXTURE)

let failures = 0
const check = (name, condition, detail = '') => {
  console.log(`${condition ? 'ok  ' : 'FAIL'} ${name}`)
  if (!condition) {
    failures++
    if (detail) console.log(`      ${detail.trim()}`)
  }
}

const withFixture = (body, fn) => {
  writeFileSync(fixturePath, body)
  try {
    return fn()
  } finally {
    try { execFileSync(process.execPath, ['-e', `require('fs').unlinkSync(${JSON.stringify(fixturePath)})`]) } catch {}
  }
}

console.log('--- the fixtures themselves must pass ---')
const good = withFixture(GOOD, () => run(FIXTURE))
check('a valid migration passes', good.code === 0, good.out)

console.log('\n--- defect 1: the default is the NEWEST migration, not a fixed file ---')
const newest = withFixture(GOOD, () => {
  // A second fixture that sorts AFTER the one under test and is genuinely broken:
  // it creates an index its Down never removes. If the default is "newest", this
  // is the file that gets checked and the run fails. A fixture that merely looked
  // suspicious would not do: `SELECT 1;` with nothing created is a valid
  // migration, which is why the first attempt at this case passed.
  const later = '9999_zzz_later_broken.sql'
  writeFileSync(
    path.join(migDir, later),
    '-- 9999_zzz_later_broken.sql\nCREATE INDEX idx_later_leak ON fixtures (id);\n\n-- +goose Down\nSELECT 1;\n'
  )
  try {
    return run(undefined)
  } finally {
    try { execFileSync(process.execPath, ['-e', `require('fs').unlinkSync(${JSON.stringify(path.join(migDir, later))})`]) } catch {}
  }
})
check(
  'with no argument the newest migration is checked',
  newest.out.includes('checking the newest migration') && newest.code !== 0,
  `expected the deliberately-broken later file to fail the run; got code ${newest.code}\n${newest.out}`
)

console.log('\n--- defect 2: -- +goose Up is optional, and a repeated Down is a bug ---')
const noUp = withFixture(GOOD, () => run(FIXTURE))
check('a migration with no -- +goose Up marker passes', noUp.code === 0, noUp.out)

const twoDown = withFixture(GOOD.replace('-- +goose Down', '-- +goose Down\nSELECT 1;\n-- +goose Down'), () => run(FIXTURE))
check('a migration with two Down markers is rejected', twoDown.code !== 0, twoDown.out)

console.log('\n--- defect 3: dollar-quote balance handles FUNCTION bodies ---')
const balanced = withFixture(GOOD, () => run(FIXTURE))
check('a CREATE FUNCTION ... AS $$ ... END $$; body is balanced', balanced.code === 0, balanced.out)

const unbalanced = withFixture(GOOD.replace('END $$;', 'RETURN NULL;'), () => run(FIXTURE))
check('an unclosed dollar-quote block is rejected', unbalanced.code !== 0, unbalanced.out)

console.log('\n--- defect 4: index reversibility sees indexes created WITHOUT IF NOT EXISTS ---')
const noIfNotExists = GOOD.replace('CREATE INDEX IF NOT EXISTS idx_fixtures_a', 'CREATE INDEX idx_fixtures_a')
const plain = withFixture(noIfNotExists, () => run(FIXTURE))
check('a plain CREATE INDEX still passes when Down drops it', plain.code === 0, plain.out)

const leakedBody = noIfNotExists.replace('DROP INDEX IF EXISTS idx_fixtures_a;', 'SELECT 1;')
const leaked = withFixture(leakedBody, () => run(FIXTURE))
check('a leaked index is rejected when created without IF NOT EXISTS', leaked.code !== 0, leaked.out)

check(
  'an undeclared non-destructive rollback is rejected',
  leaked.code !== 0,
  'the same fixture is the undeclared-retention case: no marker, index not dropped'
)

const retained = withFixture(
  noIfNotExists.replace(
    'DROP INDEX IF EXISTS idx_fixtures_a;',
    '-- goose-down: retained\nSELECT 1;'
  ),
  () => run(FIXTURE)
)
check('a DECLARED non-destructive rollback is accepted', retained.code === 0, retained.out)
console.log('\n--- a DROP TABLE removes that table\'s indexes ---')
const tableOwned = withFixture(
  `-- 9999_fixtures.sql
CREATE TABLE fixtures (id INT PRIMARY KEY);
CREATE INDEX idx_fixtures_b ON fixtures (id);

-- +goose Down
DROP TABLE IF EXISTS fixtures;
`,
  () => run(FIXTURE)
)
check(
  'an index on a table that Down drops does not need its own DROP INDEX',
  tableOwned.code === 0,
  tableOwned.out
)

console.log(`\n${failures === 0 ? 'PASS' : 'FAIL'}: ${failures} problem(s)`)
process.exit(failures ? 1 : 0)
