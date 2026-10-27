const test = require('node:test');
const assert = require('node:assert/strict');

const selectors = require('../src/selectors.js');

const tile = ({ name, text = '' }) => ({
  innerText: text,
  querySelector(selector) {
    if (selector !== selectors.nameSel || name === undefined) return null;
    return { innerText: name };
  }
});

test('collapses repeated Sruti dedicated-name lines', () => {
  assert.equal(selectors.nameOf(tile({ name: 'Sruti\nSruti' })), 'Sruti');
});

test('collapses repeated Yatin T dedicated-name lines', () => {
  assert.equal(selectors.nameOf(tile({ name: 'Yatin T\nYatin T' })), 'Yatin T');
});

test('returns null for controls-only tile text', () => {
  const text = 'frame_person Reframe visual_effects Backgrounds and effects More options for Yatin T';
  assert.equal(selectors.nameOf(tile({ text })), null);
});

test('pickName returns the first dedicated participant name', () => {
  const controls = tile({
    text: 'frame_person Reframe visual_effects Backgrounds and effects More options for Yatin T'
  });
  assert.equal(selectors.pickName([controls, tile({ name: 'Sruti' })]), 'Sruti');
});
