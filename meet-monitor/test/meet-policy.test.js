const test = require('node:test');
const assert = require('node:assert/strict');

const {
  DEFAULT_MEET_CODES,
  meetingCode,
  normalizeMeetCodes,
  isMonitoredMeetURL
} = require('../src/meet-policy.js');

test('normalizes comma-separated Meet codes and removes duplicates', () => {
  assert.deepEqual(normalizeMeetCodes(
    'jpy-ymgz-tdo, abc-defg-hij,\njpy-ymgz-tdo'
  ), [
    'jpy-ymgz-tdo',
    'abc-defg-hij'
  ]);
});

test('normalizes previously saved Meet URLs to codes', () => {
  assert.deepEqual(normalizeMeetCodes([
    'https://meet.google.com/jpy-ymgz-tdo',
    'https://meet.google.com/abc-defg-hij?authuser=1'
  ]), ['jpy-ymgz-tdo', 'abc-defg-hij']);
});

test('rejects malformed Meet codes', () => {
  for (const value of [
    'https://example.com/jpy-ymgz-tdo',
    'http://meet.google.com/jpy-ymgz-tdo',
    'https://meet.google.com/not-a-code'
  ]) {
    assert.throws(() => normalizeMeetCodes(value));
  }
});

test('matches only configured Meet pages', () => {
  const codes = ['jpy-ymgz-tdo'];
  assert.equal(isMonitoredMeetURL('https://meet.google.com/jpy-ymgz-tdo?authuser=1', codes), true);
  assert.equal(
    isMonitoredMeetURL(
      'https://meet.google.com/meet/jpy-ymgz-tdo?pli=1&authuser=4',
      codes
    ),
    true
  );
  assert.equal(isMonitoredMeetURL('https://meet.google.com/abc-defg-hij', codes), false);
  assert.equal(meetingCode(codes[0]), 'jpy-ymgz-tdo');
  assert.deepEqual(DEFAULT_MEET_CODES, ['jpy-ymgz-tdo']);
});
