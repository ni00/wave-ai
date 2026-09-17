// Localize only documentation. The native English OpenAPI contract owns all values.
const fs = require('node:fs');
const literalKeys = new Set(['example', 'examples', 'default', 'enum', 'const']);
const escape = key => String(key).replaceAll('~', '~0').replaceAll('/', '~1');
function localize(document, translations) {
  const used = new Set();
  function walk(value, path = '') {
    if (Array.isArray(value)) return value.map((item, index) => walk(item, `${path}/${index}`));
    if (!value || typeof value !== 'object') return value;
    return Object.fromEntries(Object.entries(value).map(([key, child]) => {
      if (literalKeys.has(key)) return [key, structuredClone(child)];
      const next = `${path}/${escape(key)}`;
      if (['summary', 'description'].includes(key) && typeof child === 'string') {
        if (typeof translations[next] !== 'string' || !translations[next].trim()) throw new Error(`missing translation at ${next}`);
        used.add(next);
        return [key, translations[next]];
      }
      return [key, walk(child, next)];
    }));
  }
  const result = walk(document);
  for (const key of Object.keys(translations)) if (!used.has(key)) throw new Error(`unused translation at ${key}`);
  return result;
}
module.exports = {localize};
if (require.main === module) {
  const [input, translations, output] = process.argv.slice(2);
  fs.writeFileSync(output, JSON.stringify(localize(JSON.parse(fs.readFileSync(input)), JSON.parse(fs.readFileSync(translations))), null, 2) + '\n');
}
