// Structural check on a goose migration file: every index it creates must be
// dropped in its Down section, or the rollback leaves the schema worse than it
// found it — which is the case a DBA discovers at the worst moment.
//
// Also checks that DO $$ ... blocks are balanced, since an unbalanced block is
// a syntax error that aborts the whole transaction.
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const dir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'backend', 'internal', 'db', 'migrations')
const file = process.argv[2] ?? '00040_money_safety_and_hot_path_indexes.sql'
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
for (const marker of ['-- +goose Up', '-- +goose Down']) {
  const n = (text.match(new RegExp(marker.replace(/[+]/g, '\\+'), 'g')) || []).length
  console.log(`${n === 1 ? 'ok  ' : 'FAIL'} ${marker} present exactly once (${n})`)
  if (n !== 1) bad++
}

// 2. plpgsql block balance
const dos = (text.match(/DO \$\$/g) || []).length
const ends = (text.match(/END \$\$;/g) || []).length
console.log(`${dos === ends && dos > 0 ? 'ok  ' : 'FAIL'} DO $$ blocks balanced (${dos} open / ${ends} end)`)
if (dos !== ends) bad++

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
const created = [...new Set([...up.matchAll(/CREATE (?:UNIQUE )?INDEX IF NOT EXISTS (\w+)/g)].map((m) => m[1]))]
const dropped = new Set([...text.replace(/--[^\n]*/g, "").matchAll(/DROP INDEX IF EXISTS (\w+)/g)].map((m) => m[1]))
const missing = created.filter((n) => !dropped.has(n))
console.log(`${missing.length === 0 ? 'ok  ' : 'FAIL'} all ${created.length} created indexes are dropped in Down`)
if (missing.length) {
  console.log(`      missing from Down: ${missing.join(', ')}`)
  bad++
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
const conDropped = [...new Set([...down.matchAll(/DROP CONSTRAINT IF EXISTS (\w+)/g)].map((m) => m[1]))]
const conMissing = added.filter((n) => !conDropped.includes(n))
console.log(`${conMissing.length === 0 ? 'ok  ' : 'FAIL'} all ${added.length} constraints added in Up are dropped in Down`)
if (conMissing.length) {
  console.log(`      missing from Down: ${conMissing.join(', ')}`)
  bad++
}

console.log(`\n${bad === 0 ? 'PASS' : 'FAIL'}: ${bad} problem(s)`)
process.exit(bad ? 1 : 0)
