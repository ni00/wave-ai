const {test} = require('node:test');
const assert = require('node:assert/strict');
const {localize} = require('./localize.cjs');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

test('translate documentation while preserving examples and contract values', () => {
  const source = {
    summary: '创建任务 || Create a task',
    description: '第一行 || First line\n\n```json\n{"id":"task_1"}\n```',
    properties: {description: {type: 'string', description: '输入 || Input'}},
    required: ['description'],
    example: {description: '示例原文 || Literal example'},
    enum: ['中文枚举'],
  };
  const en = localize(source, 'en');
  assert.equal(en.summary, 'Create a task');
  assert.equal(en.properties.description.description, 'Input');
  assert.equal(en.description, 'First line\n\n```json\n{"id":"task_1"}\n```');
  assert.deepEqual(en.example, source.example);
  assert.deepEqual(en.enum, source.enum);
  assert.equal(localize(source, 'zh-CN').summary, '创建任务');
  assert.equal(source.summary, '创建任务 || Create a task');
});

test('reject missing or malformed translations instead of silently falling back', () => {
  for (const description of ['仅中文', '中文 || ', '中文 || English || Extra', '中文 || 仍是中文']) {
    assert.throws(() => localize({description}, 'en'));
  }
  assert.throws(() => localize({}, 'fr'));
});

test('Swagger initializer selects the page language and exposes both contracts', () => {
  const script = readFileSync(require.resolve('../../api/swagger-initializer.js'), 'utf8');
  for (const [search, selected, htmlLanguage] of [['', 'English', 'en'], ['?lang=en', 'English', 'en'], ['?lang=zh-CN', '中文', 'zh-CN'], ['?lang=unknown', 'English', 'en']]) {
    let options;
    const bundle = config => { options = config; return {}; };
    bundle.presets = {apis: {}};
    bundle.plugins = {DownloadUrl: {}};
    const context = {window: {location: {search}}, document: {documentElement: {}}, URLSearchParams, SwaggerUIBundle: bundle, SwaggerUIStandalonePreset: {}};
    vm.runInNewContext(script, context);
    context.window.onload();
    assert.equal(options['urls.primaryName'], selected);
    assert.equal(options.urls.length, 2);
    assert.equal(options.urls.find(item => item.name === selected).url, selected === 'English' ? '/swagger/openapi.json' : '/swagger/openapi.zh-CN.json');
    assert.equal(context.document.documentElement.lang, htmlLanguage);
    assert.equal(options.validatorUrl, null);
    assert.equal(options.persistAuthorization, false);
  }
});
