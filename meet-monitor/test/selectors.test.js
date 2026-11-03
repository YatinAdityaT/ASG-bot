const test = require('node:test');
const assert = require('node:assert/strict');

const selectors = require('../src/selectors.js');

const tile = ({ name, text = '', nameTextContent }) => ({
  innerText: text,
  querySelector(selector) {
    assert.equal(selector, 'div[jsname="giiMnc"]');
    if (name === undefined && nameTextContent === undefined) return null;
    return { innerText: name, textContent: nameTextContent };
  }
});

test('collapses repeated Sruti dedicated-name lines', () => {
  assert.equal(selectors.nameOf(tile({ name: 'Sruti\nSruti' })), 'Sruti');
});

test('collapses repeated Yatin T dedicated-name lines', () => {
  assert.equal(selectors.nameOf(tile({ name: 'Yatin T\nYatin T' })), 'Yatin T');
});

test('extracts a dedicated participant name available only through textContent', () => {
  assert.equal(selectors.nameOf(tile({ nameTextContent: 'Alex' })), 'Alex');
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

const mediaTile = ({ labels = [], videos = [], mutedIcon = false }) => ({
  querySelector(selector) {
    if (selector === selectors.muteIconPathSel) return mutedIcon ? {} : null;
    return null;
  },
  querySelectorAll(selector) {
    if (selector === '[aria-label]') {
      return labels.map(ariaLabel => ({
        getAttribute: attribute => attribute === 'aria-label' ? ariaLabel : null
      }));
    }
    if (selector === selectors.videoSel) return videos;
    return [];
  }
});

test('detects a muted microphone from the current Meet control label', () => {
  assert.equal(selectors.micOf([
    mediaTile({ labels: ["You can't unmute someone else"] })
  ]), 'muted');
});

test('detects an unmuted microphone from the current Meet control label', () => {
  assert.equal(selectors.micOf([
    mediaTile({ labels: ["You can't mute Yatin T's microphone remotely"] })
  ]), 'unmuted');
});

test('detects an active camera when a later video element is playing', () => {
  assert.equal(selectors.camOf(mediaTile({
    videos: [
      { readyState: 0, videoWidth: 0 },
      { readyState: 4, videoWidth: 640 }
    ]
  })), true);
});
