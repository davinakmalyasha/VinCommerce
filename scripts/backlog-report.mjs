// Reports the created backlog: issue count per milestone, and any issue that
// did not land in a milestone.
import { execFileSync } from "node:child_process"
const REPO = 'davinakmalyasha/VinCommerce'

const ms = JSON.parse(
  execFileSync('gh', ['api', `repos/${REPO}/milestones`, '--paginate'], { encoding: 'utf8', maxBuffer: 1 << 24 }),
)
const byNumber = new Map(ms.map((m) => [m.number, m.title]))
console.log('MILESTONES:')
for (const m of ms) console.log(`  #${m.number} ${m.title} (${m.open_issues} open, ${m.closed_issues} closed)`)

const issues = JSON.parse(
  execFileSync(
    'gh',
    ['issue', 'list', '--repo', REPO, '--state', 'all', '--limit', '100', '--json', 'number,title,milestone,labels'],
    { encoding: 'utf8', maxBuffer: 1 << 24 },
  ),
)

const groups = new Map()
const unassigned = []
for (const i of issues) {
  const id = (i.title.match(/^\[([^\]]+)\]/) || [])[1] ?? '?'
  const m = i.milestone?.title ?? null
  if (!m) unassigned.push(`${id} #${i.number}`)
  if (!groups.has(m)) groups.set(m, [])
  groups.get(m).push(`${id} ${i.labels.map((l) => l.name).join('/')}`)
}

console.log(`\nISSUES (${issues.length}):`)
for (const [m, list] of [...groups.entries()].sort()) {
  console.log(`\n  ${m ?? '(no milestone)'} — ${list.length}`)
  for (const s of list.sort()) console.log(`    ${s}`)
}
if (unassigned.length) {
  console.log(`\nUNASSIGNED (${unassigned.length}):`)
  for (const u of unassigned) console.log(`  ${u}`)
} else {
  console.log('\nAll issues have a milestone.')
}
