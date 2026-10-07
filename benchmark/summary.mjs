// Prints results.json as markdown tables, the ones of visualizer.html:
//   node benchmark/summary.mjs >> "$GITHUB_STEP_SUMMARY"
// .mjs, so that Node loads this file and report.js as modules without a package.json
// (report.js keeps its extension: the browser needs it served as JavaScript).
import { readFileSync } from 'node:fs';
import {
  CASES, LOAD_COLUMNS, YAEGI_COLUMNS, YAEGI_NOTE,
  backends, bytes, duration, gain, loadNote, milliseconds, number, rate, warning,
} from './report.js';

const raw = readFileSync(new URL('./results.json', import.meta.url), 'utf8');
const results = JSON.parse(raw);

function table(columns, rows) {
  const line = (cells) => `| ${cells.join(' | ')} |`;
  return [
    line(columns.map(([label]) => label)),
    line(columns.map(([, className]) => (className === 'num' ? '--:' : '---'))),
    ...rows.map(line),
  ].join('\n');
}

const load = [];
for (const backend of backends(results.k6)) {
  Object.entries(results.k6[backend]).forEach(([name, result], i) => {
    const alert = warning(result);
    load.push([
      i === 0 ? `**${backend}**` : '',
      CASES[name] || name,
      `\`${result.url}\``,
      rate(result.requests_per_second),
      milliseconds(result.p50_ms),
      milliseconds(result.p95_ms),
      (result.throughput_vs_direct === undefined ? gain() : `**${gain(result.throughput_vs_direct)}**`) +
        (alert ? `<br>${alert}` : ''),
    ]);
  });
}

const yaegi = Object.entries(results.yaegi).map(([name, result]) => [
  `\`${name}\``,
  duration(result.ns_per_op),
  bytes(result.bytes_per_op),
  number(result.allocs_per_op, 0),
]);

console.log(`## Traedis benchmarks

### Load test through Traefik

${loadNote(results.k6)}

${table(LOAD_COLUMNS, load)}

### Handler under Yaegi

${YAEGI_NOTE}

${table(YAEGI_COLUMNS, yaegi)}

<details>
<summary>results.json</summary>

\`\`\`json
${raw.trimEnd()}
\`\`\`

</details>`);
