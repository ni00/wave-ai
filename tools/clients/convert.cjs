// The service annotations remain the source of HTTP paths and schemas.
const fs = require('node:fs');
const converter = require('swagger2openapi');
const {localize} = require('./localize.cjs');
const [input, output, language = 'en'] = process.argv.slice(2);
converter.convertObj(JSON.parse(fs.readFileSync(input, 'utf8')), {}, (error, result) => {
  if (error) throw error;
  const spec = localize(result.openapi, language);
  spec.servers = [{url: 'http://localhost:8080'}];
  spec.components.securitySchemes.BearerAuth = {type: 'http', scheme: 'bearer'};
  spec.info.description = spec.info.description.replace('Authorization 填写完整的 `Bearer <Wave API Key>`。', 'SDK 和 Swagger UI 的 bearer 配置填写原始 Wave API Key。')
    .replace('可空字段通过 x-nullable 标记', '可空字段通过 nullable 标记')
    .replace('OpenAPI spec 见 /swagger/doc.json', 'OpenAPI 3 spec 见 /swagger/openapi.zh-CN.json；旧 Swagger 2 见 /swagger/doc.json')
    .replace('Set Authorization to the full `Bearer <Wave API Key>` value.', 'Enter the raw Wave API Key in the SDK and Swagger UI bearer configuration.')
    .replace('Nullable fields use x-nullable', 'Nullable fields use nullable')
    .replace('OpenAPI spec: /swagger/doc.json.', 'OpenAPI 3 spec: /swagger/openapi.json; legacy Swagger 2: /swagger/doc.json.');
  spec.info.description += '\n\n[English](/swagger/index.html?lang=en) · [中文](/swagger/index.html?lang=zh-CN)';
  for (const methods of Object.values(spec.paths)) {
    for (const op of Object.values(methods)) {
      if (!op.operationId) continue;
      for (const p of op.parameters || []) {
        if (p.name === 'after') delete p.schema.default;
      }
      for (const [status, response] of Object.entries(op.responses)) {
        if (!response.content) continue;
        let media = ['application/json'];
        if (['filesContent', 'skillsContent'].includes(op.operationId)) {
          if (status === '200') media = ['application/octet-stream'];
          if (status === '206') media = ['application/octet-stream', 'multipart/byteranges'];
          if (status === '416') media = ['text/plain'];
        }
        if (op.operationId === 'executionStreamEvents' && status === '200') media = ['text/event-stream'];
        response.content = Object.fromEntries(media.map(type => {
          if (!response.content[type]) throw new Error(`missing ${op.operationId}/${status}/${type}`);
          return [type, response.content[type]];
        }));
      }
    }
  }
  // Keep the generator-friendly object; preserve the server's additional
  // field-presence rules as metadata consumed by the public runtimes.
  spec.components.schemas['execution.ToolResultRequest']['x-wave-validation'] = 'approval-or-result';
  fs.writeFileSync(output, JSON.stringify(spec, null, 2) + '\n');
});
