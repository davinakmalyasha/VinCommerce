// Measures the true initial-payload size of the built app: the entry chunk
// plus everything index.html preloads. This is what a first-time visitor
// downloads before the app can paint.
const z = require('zlib')
const fs = require('fs')
const path = require('path')

const dist = path.join(__dirname, '..', 'dist')
const html = fs.readFileSync(path.join(dist, 'index.html'), 'utf8')

const assets = [...new Set([...html.matchAll(/\/assets\/[^"']+\.(js|css)/g)].map((m) => m[0]))]

let raw = 0
let gz = 0
for (const p of assets.sort()) {
  const buf = fs.readFileSync(path.join(dist, p))
  const g = z.gzipSync(buf, { level: 9 }).length
  raw += buf.length
  gz += g
  const kb = (n) => `${(n / 1024).toFixed(1).padStart(7)} KB`
  console.log(`${kb(buf.length)} raw  ${(g / 1024).toFixed(1).padStart(6)} KB gz  ${p.split('/').pop()}`)
}
console.log('-'.repeat(58))
console.log(`${(raw / 1024).toFixed(1).padStart(7)} KB raw  ${(gz / 1024).toFixed(1).padStart(6)} KB gz  TOTAL initial (${assets.length} files)`)
