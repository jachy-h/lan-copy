const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../web/filesystem.js'), 'utf8');
const start = source.indexOf('const RECENT_WINDOW_MS');
const end = source.indexOf('\nfunction updateFreshTags()', start);
assert(start >= 0 && end > start, 'recent-file check should remain a small standalone helper');
const isRecent = vm.runInNewContext(`${source.slice(start, end)}\nisRecent`, {Date, Number});
const now = Date.parse('2026-10-23T12:00:00.000Z');
const iso = timestamp => new Date(timestamp).toISOString();

assert.equal(isRecent(iso(now), now), true, 'zero-hour age is recent');
assert.equal(isRecent(iso(now - 4 * 60 * 60 * 1000 + 1), now), true, 'age just below four hours is recent');
assert.equal(isRecent(iso(now - 4 * 60 * 60 * 1000), now), false, 'exactly four hours is not recent');
assert.equal(isRecent(iso(now - 4 * 60 * 60 * 1000 - 1), now), false, 'age above four hours is not recent');
assert.equal(isRecent(iso(now + 1), now), false, 'future timestamps are not recent');
assert.equal(isRecent('', now), false, 'missing timestamps are not recent');
assert.equal(isRecent('invalid', now), false, 'invalid timestamps are not recent');

console.log('Filesystem time checks passed: recent window boundaries, future and invalid timestamps.');
