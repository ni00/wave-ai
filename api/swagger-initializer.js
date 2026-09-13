// Read the page query: the browser requests this script without that query.
window.onload = function () {
  const language = new URLSearchParams(window.location.search).get('lang');
  const selected = language === 'zh-CN' ? '中文' : 'English';
  document.documentElement.lang = language === 'zh-CN' ? 'zh-CN' : 'en';
  window.ui = SwaggerUIBundle({
    urls: [
      {name: 'English', url: '/swagger/openapi.json'},
      {name: '中文', url: '/swagger/openapi.zh-CN.json'},
    ],
    'urls.primaryName': selected,
    dom_id: '#swagger-ui',
    validatorUrl: null,
    persistAuthorization: false,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    plugins: [SwaggerUIBundle.plugins.DownloadUrl],
    layout: 'StandaloneLayout',
    docExpansion: 'list',
    deepLinking: true,
    defaultModelsExpandDepth: 1,
  });
};
