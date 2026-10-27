const test = require('node:test');
const assert = require('node:assert/strict');

const { row } = require('../src/csv.js');

test('CSV rows quote commas, quotes, and newlines correctly', () => {
  assert.equal(
    row(['plain', 'comma,value', 'say "hello"', 'two\nlines']),
    'plain,"comma,value","say ""hello""","two\nlines"'
  );
});
