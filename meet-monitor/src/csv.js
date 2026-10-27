(function (root, factory) {
  const api = factory();
  root.MMCSV = api;
  if (typeof module === 'object' && module.exports) module.exports = api;
})(typeof globalThis === 'object' ? globalThis : this, () => {
  const cell = value => {
    const text = String(value ?? '');
    return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
  };

  return {
    row: values => values.map(cell).join(',')
  };
});
