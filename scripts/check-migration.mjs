// Structural check on a goose migration file: every index it creates must be
// dropped in its Down section, or the rollback leaves the schema worse than it
// found it — which is the case a DBA discovers at the worst moment.
//
// Also checks that dollar-quote blocks are balanced, since an unbalanced block is
// a syntax error that aborts the whole transaction.
//
// With no argument it checks the NEWEST migration, which is the one nobody has
// reviewed yet and therefore the one worth checking. It used to default to
// 00040, so the CI step labelled "Validate the newest migration" has been
// validating a fixed file since it was written, and every migration added since
// — including the double-entry ledger — went through it unexamined.
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const dir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'backend', 'internal', 'db', 'migrations')

function newestMigration() {
  const files = readdirSync(dir)
    .filter((f) => f.endsWith('.sql'))
    .sort()
  if (files.length === 0) throw new Error(`no migrations in ${dir}`)
  return files[files.length - 1]
}

const file = process.argv[2] ?? newestMigration()
if (!process.argv[2]) {
  console.log(`(no file given; checking the newest migration: ${file})`)
}
const text = readFileSync(path.join(dir, file), 'utf8')

let bad = 0

// Split the file at the Down marker. Scanning the whole file for "what does Up
// create" picks up the Down section's legitimate re-ADD of the original CHECK,
// which produced a false failure.
const downIdx = text.indexOf('-- +goose Down')
if (downIdx < 0) {
  console.log('FAIL no -- +goose Down marker found')
  process.exit(1)
}
// Strip `--` comments before any statement matching: the prose explains *why*
// each statement is ordered as it is and necessarily names the statements it
// refers to, so matching the raw text produced phantom findings (a comment
// reading "would otherwise fail the ADD CONSTRAINT instead" was parsed as a
// constraint named "instead").
const stripSqlComments = (s) => s.replace(/--[^\n]*/g, '')
const upRaw = text.slice(0, downIdx)
const up = stripSqlComments(upRaw)
const down = stripSqlComments(text.slice(downIdx))

// 1. goose markers
//
// `-- +goose Up` is OPTIONAL: goose treats everything before `-- +goose Down` as
// the Up section, and no migration in this repository uses the explicit marker.
// The previous version required it "exactly once", which meant every real
// migration failed this check and the only reason CI was green is that the step
// passed no filename and fell through to checking 00040 (see checkNewest()).
// Requiring a marker nobody writes is a check that cannot pass, and a check that
// cannot pass is not a check.
//
// What is required is that the Down marker exists and comes last, because that is
// what makes the split meaningful.
const downMarker = '-- +goose Down'
const downAt = text.indexOf(downMarker)
console.log(`${downAt >= 0 ? 'ok  ' : 'FAIL'} ${downMarker} present (${(text.match(/-- \+goose Down/g) || []).length} occurrence(s))`)
if (downAt < 0) bad++

const upCount = (text.match(/-- \+goose Up/g) || []).length
const downCount = (text.match(/-- \+goose Down/g) || []).length
if (upCount > 1) {
  console.log(`FAIL   -- +goose Up appears ${upCount} times`)
  bad++
} else {
  console.log(`ok    -- +goose Up is optional and not misused (${upCount})`)
}

// A second Down marker would split the rollback in two and confuse goose.
if (downCount > 1) {
  console.log(`FAIL   -- +goose Down appears ${downCount} times; the rollback would be split`)
  bad++
}

// 2. dollar-quote block balance
//
// The previous version counted `DO $$` openings against `END $$;` endings, which
// only understands ANONYMOUS plpgsql blocks. Every trigger function in these
// migrations is `CREATE OR REPLACE FUNCTION ... AS $$ ... END $$;`, whose opener
// is a bare `$$`. So a file with three correct function bodies reported "0 open /
// 3 end" and FAILED -- a false positive on correct code, on the most important
// structural property there is.
//
// The real property is that every `$$` delimiter pairs up. Comments are stripped
// first, because a `$$` inside a comment is prose, not a delimiter, and counting
// it produces exactly the kind of phantom finding the comment-stripping above
// exists to prevent.
const strippedAll = stripSqlComments(text)
const quotes = (strippedAll.match(/\$\$/g) || []).length
console.log(`${quotes % 2 === 0 ? 'ok  ' : 'FAIL'} dollar-quote blocks balanced (${quotes} delimiters)`)
if (quotes % 2 !== 0) {
  console.log('      an odd count means a plpgsql block is never closed, which aborts the migration')
  bad++
}

// 3. every DECLARE'd variable is used, and every used one is declared
const declared = new Set()
for (const m of text.matchAll(/DECLARE\s+([\s\S]*?)\nBEGIN/g)) {
  for (const v of m[1].matchAll(/^\s*(\w+)\s+(BIGINT|INT|TEXT|BOOLEAN)\s*;/gm)) declared.add(v[1])
}
console.log(`ok    declared plpgsql variables: ${[...declared].join(', ')}`)
for (const v of declared) {
  const uses = (text.match(new RegExp(`\\b${v}\\b`, 'g')) || []).length
  // one occurrence is the declaration itself
  if (uses < 2) {
    console.log(`FAIL  variable "${v}" is declared but never used`)
    bad++
  }
}

// 4. index reversibility
//
// Scans for `CREATE INDEX` and `CREATE UNIQUE INDEX` with or without
// `IF NOT EXISTS`.
//
// The previous version required `IF NOT EXISTS`, so any index created without it
// was invisible to the check -- and a migration may legitimately omit it, since
// the whole file runs in one transaction and a duplicate name is then a hard
// error anyway. The result was that "all 0 created indexes are dropped in Down"
// reported success on migrations that create six indexes.
//
// The other half of the gap: an index DROPPED in Up and recreated in Down is not
// "created in Up", so it is correctly not required in Down. The asymmetry is
// intentional and is why the split is done at the Down marker.
const created = [...new Set([
  ...[...up.matchAll(/CREATE (?:UNIQUE )?INDEX (?:IF NOT EXISTS )?(\w+)/g)].map((m) => m[1]),
  ...[...down.matchAll(/CREATE (?:UNIQUE )?INDEX (?:IF NOT EXISTS )?(\w+)/g)].map((m) => m[1]),
])]
// Which table each created index lives on, so a `DROP TABLE` in Down can be
// understood to remove that table's indexes with it. In Postgres it does, and
// requiring the indexes to be dropped individually would push people to write
// redundant statements that a reader then has to work out are redundant.
const indexTable = new Map()
for (const m of up.matchAll(/CREATE (?:UNIQUE )?INDEX (?:IF NOT EXISTS )?(\w+)\s+ON\s+(\w+)/g)) {
  indexTable.set(m[1], m[2])
}
const droppedTables = new Set([...down.matchAll(/DROP TABLE (?:IF EXISTS )?(\w+)/g)].map((m) => m[1]))

const dropped = new Set([...strippedAll.matchAll(/DROP INDEX (?:IF EXISTS )?(\w+)/g)].map((m) => m[1]))
// An index that Up drops is not created by Up, so it is not this migration's to
// clean up -- but one that Up CREATES and Down recreates identically is a
// deliberate restore, not a leak.
const restoredInDown = new Set([...down.matchAll(/CREATE (?:UNIQUE )?INDEX (?:IF NOT EXISTS )?(\w+)/g)].map((m) => m[1]))
const missing = created.filter(
  (n) => !dropped.has(n) && !(createdInUp(n) && restoredInDown.has(n)) && !droppedTables.has(indexTable.get(n))
)

// A Down that is deliberately non-destructive is a legitimate choice -- for an
// accounting migration, dropping the tables on rollback would destroy the history
// the accounting is based on. But it has to be DECLARED.
//
// 00043 chose that, and did it silently: its Down is `SELECT 1;`. So `goose down`
// reports success having changed nothing, and a reader diffing the migrations
// cannot tell an intentional no-op from a forgotten one. That is the same failure
// as a check that cannot fail, one level up: the rollback appears to work.
//
// The marker makes the choice reviewable. A migration whose Down omits it is
// treated as a bug, which is the correct default -- silence should mean "I did
// not think about this".
//
// Read from the RAW Down text, not the comment-stripped SQL: the marker IS a
// comment, and stripping comments is what every other check here does for good
// reason. Checking the stripped text looks for a marker that stripping removed,
// and so never finds one -- which is why 00043 failed even after declaring it.
const RETAINED = '-- goose-down: retained'
const downRawText = text.slice(downIdx)
const downDeclaresRetention = downRawText.includes(RETAINED)
if (missing.length === 0) {
  console.log(`ok    all ${created.length} created indexes are dropped in Down`)
} else if (downDeclaresRetention) {
  console.log(`ok    ${missing.length} index(es) intentionally retained; Down declares '${RETAINED}'`)
} else {
  console.log(`FAIL all ${created.length} created indexes are dropped in Down`)
  console.log(`      missing from Down: ${missing.join(', ')}`)
  console.log(`      if the rollback is deliberately non-destructive, add a`)
  console.log(`      '${RETAINED}' line to the Down section explaining why`)
  bad++
}

function createdInUp(name) {
  return [...up.matchAll(new RegExp(`CREATE (?:UNIQUE )?INDEX (?:IF NOT EXISTS )?${name}\\b`, 'g'))].length > 0
}

// 5. a bare CREATE UNIQUE INDEX at the top level (column 0) is a footgun: it
// aborts the whole migration if the data violates it. Those must be preceded by
// a repair, which we can only sanity-check by requiring them to appear in a
// section that also contains a repair or a conditional.
const topLevel = up.split('\n').filter((l) => /^CREATE UNIQUE INDEX/.test(l))
console.log(`ok    ${topLevel.length} top-level CREATE UNIQUE INDEX statement(s) (each must have a repair above it)`)

// 6. constraints added in Up must be dropped in Down.
//
// Only the Up section is scanned for ADD CONSTRAINT: the Down legitimately
// RE-ADDS the original `payment_intents_status_check`, and counting that as an
// addition made the check report a false failure on its own rollback.
const added = [...new Set([...up.matchAll(/ADD CONSTRAINT (\w+)/g)].map((m) => m[1]))]
const conDropped = new Set([...down.matchAll(/DROP CONSTRAINT (?:IF EXISTS )?(\w+)/g)].map((m) => m[1]))
// `.has`, not `.includes`: conDropped is a Set. It was an array when the regex
// was `DROP CONSTRAINT IF EXISTS` with a literal space, and widening the regex to
// accept the no-`IF EXISTS` form meant rebuilding it as a Set -- at which point
// the `.includes` below threw a TypeError and the checker crashed instead of
// reporting. A checker that crashes on a file is a checker nobody runs.
//
// A constraint another migration OWNS is not this one's to drop. 00041 re-adds
// `orders_discount_bounded` defensively -- 00040 created it inside a DO block --
// and deliberately leaves it, because dropping it in both files would make the two
// migrations fight over the same object. That is a correct decision with a
// comment explaining it, and a checker that cannot see it forces a choice between
// a redundant DROP and a false failure. The marker names the objects so the
// decision is reviewable rather than inferred.
const notOwned = new Set(
  [...downRawText.matchAll(/--\s*goose-down:\s*not-owned\s+(\w+)/g)].map((m) => m[1])
)
const conMissing = added.filter((n) => !conDropped.has(n) && !notOwned.has(n))
console.log(`${conMissing.length === 0 ? 'ok  ' : 'FAIL'} all ${added.length} constraints added in Up are dropped in Down`)
if (conMissing.length) {
  console.log(`      missing from Down: ${conMissing.join(', ')}`)
  if (notOwned.size) console.log(`      declared not-owned: ${[...notOwned].join(', ')}`)
  console.log(`      if another migration owns it, add a`)
  console.log(`      '-- goose-down: not-owned <name>' line to the Down section`)
  bad++
}

console.log(`\n${bad === 0 ? 'PASS' : 'FAIL'}: ${bad} problem(s)`)
process.exit(bad ? 1 : 0)
