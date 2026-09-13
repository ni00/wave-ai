// Source annotations use one Chinese/English pair per line: 中文 || English.
// Identifiers, examples and schema values are never translated.
const separator = ' || ';
const han = /\p{Script=Han}/u;
const literalKeys = new Set(['example', 'examples', 'default', 'enum', 'const']);

function localize(document, language) {
  if (!['zh-CN', 'en'].includes(language)) throw new Error(`unsupported language: ${language}`);
  function text(value, path) {
    return value.split('\n').map(line => {
      const pair = line.split(separator);
      if (pair.length === 1) {
        if (han.test(line)) throw new Error(`missing English annotation at ${path}: ${line}`);
        return line;
      }
      if (pair.length !== 2 || pair.some(part => !part.trim()) || han.test(pair[1])) {
        throw new Error(`invalid bilingual annotation at ${path}: ${line}`);
      }
      return pair[language === 'zh-CN' ? 0 : 1];
    }).join('\n');
  }
  function walk(value, path = '$') {
    if (Array.isArray(value)) return value.map((item, index) => walk(item, `${path}[${index}]`));
    if (!value || typeof value !== 'object') return value;
    return Object.fromEntries(Object.entries(value).map(([key, child]) => {
      if (literalKeys.has(key)) return [key, structuredClone(child)];
      const next = `${path}.${key}`;
      return [key, ['summary', 'description'].includes(key) && typeof child === 'string'
        ? text(child, next) : walk(child, next)];
    }));
  }
  return walk(document);
}

module.exports = {localize};
