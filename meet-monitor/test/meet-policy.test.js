const test = require('node:test');
const assert = require('node:assert/strict');

const {
  DEFAULT_MEET_LINKS,
  meetingCode,
  normalizeMeetLinks,
  isMonitoredMeetURL
} = require('../src/meet-policy.js');

test('normalizes comma-separated Meet links and removes duplicates', () => {
  assert.deepEqual(normalizeMeetLinks(
    'https://meet.google.com/jpy-ymgz-tdo, https://meet.google.com/abc-defg-hij?authuser=1,\nhttps://meet.google.com/jpy-ymgz-tdo'
  ), [
    'https://meet.google.com/jpy-ymgz-tdo',
    'https://meet.google.com/abc-defg-hij'
  ]);
});

test('rejects non-Meet and malformed links', () => {
  for (const value of [
    'https://example.com/jpy-ymgz-tdo',
    'http://meet.google.com/jpy-ymgz-tdo',
    'https://meet.google.com/not-a-code'
  ]) {
    assert.throws(() => normalizeMeetLinks(value));
  }
});

test('matches only configured Meet pages', () => {
  const links = ['https://meet.google.com/jpy-ymgz-tdo'];
  assert.equal(isMonitoredMeetURL('https://meet.google.com/jpy-ymgz-tdo?authuser=1', links), true);
  assert.equal(isMonitoredMeetURL('https://meet.google.com/abc-defg-hij', links), false);
  assert.equal(meetingCode(links[0]), 'jpy-ymgz-tdo');
  assert.deepEqual(DEFAULT_MEET_LINKS, ['https://meet.google.com/jpy-ymgz-tdo']);
});
