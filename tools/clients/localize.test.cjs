const {test} = require('node:test');
const assert = require('node:assert/strict');
const {localize} = require('./localize.cjs');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

test('translate documentation while preserving literals and escaped paths', () => {
 const source={summary:'Create',paths:{'/v1/test':{description:'Test'}},example:{description:'literal'},enum:['literal']};
 const zh=localize(source,{'/summary':'创建','/paths/~1v1~1test/description':'测试'});
 assert.equal(zh.summary,'创建');
 assert.equal(zh.paths['/v1/test'].description,'测试');
 assert.deepEqual(zh.example,source.example);
 assert.deepEqual(zh.enum,source.enum);
 assert.equal(source.summary,'Create');
});
test('reject missing and stale translations, including literal modifications', () => {
 assert.throws(()=>localize({summary:'Create'},{}));
 assert.throws(()=>localize({summary:'Create'},{'/summary':''}));
 assert.throws(()=>localize({example:{description:'literal'}},{'/example/description':'changed'}));
 assert.throws(()=>localize({}, {'/removed':'stale'}));
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
