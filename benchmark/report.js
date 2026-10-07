// What the two reports of results.json share, so that they say the same thing:
// visualizer.html (in a browser) and summary.mjs (markdown, for the CI job summary).

export const CASES = { direct: 'Without cache', hit: 'Cache hit', miss: 'Cache miss' };

// [label, 'num' for a right-aligned column]
export const LOAD_COLUMNS = [
  ['Backend'], ['Case'], ['URL'], ['Requests/s', 'num'], ['p50', 'num'], ['p95', 'num'], ['vs without cache', 'num'],
];
export const YAEGI_COLUMNS = [
  ['Benchmark'], ['Time per request', 'num'], ['Memory per request', 'num'], ['Allocations per request', 'num'],
];

export const YAEGI_NOTE = 'The middleware called in-process, against a real Redis: cost of one request.';

export function loadNote(k6) {
  return `k6, ${k6.vus} virtual users for ${k6.seconds} s per case. ` +
    'Each backend is requested on its route without the cache, then on the cached route: ' +
    'one stored URL (hit), and a new URL for every request (miss).';
}

// backends lists the backends of the k6 results (its other keys are the run's settings).
export function backends(k6) {
  return Object.keys(k6).filter((key) => typeof k6[key] === 'object');
}

export function number(value, digits) {
  return value.toLocaleString('en-US', { minimumFractionDigits: digits, maximumFractionDigits: digits });
}

export function milliseconds(ms) {
  return number(ms, ms < 10 ? 2 : ms < 100 ? 1 : 0) + ' ms';
}

export function duration(ns) {
  if (ns < 1e6) {
    return number(ns / 1e3, ns < 1e5 ? 1 : 0) + ' µs';
  }
  return milliseconds(ns / 1e6);
}

export function bytes(n) {
  if (n < 1 << 20) {
    return number(n / 1024, n < 100 << 10 ? 1 : 0) + ' KiB';
  }
  return number(n / (1 << 20), 2) + ' MiB';
}

export function rate(perSecond) {
  return number(perSecond, perSecond < 100 ? 1 : 0);
}

// gain formats throughput_vs_direct, absent from the case without the cache.
export function gain(ratio) {
  return ratio === undefined ? '—' : '×' + number(ratio, ratio < 10 ? 2 : 1);
}

// warning is not empty when some responses did not carry the expected Cache-Status.
export function warning(result) {
  return result.expected_responses < 1 ? '⚠ ' + number(100 * result.expected_responses, 0) + '% expected responses' : '';
}
