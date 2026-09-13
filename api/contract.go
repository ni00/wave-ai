// Package api contains the published OpenAPI contract consumed by the SDKs.
package api

import _ "embed"

//go:embed openapi.json
var OpenAPI []byte

//go:embed openapi.zh-CN.json
var OpenAPIChinese []byte

//go:embed swagger-initializer.js
var SwaggerInitializer []byte
