package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed swaggerui/*
var swaggerUIFS embed.FS

// SwaggerUIHandler serves the self-contained Swagger UI at /docs.
func SwaggerUIHandler() http.Handler {
	sub, err := fs.Sub(swaggerUIFS, "swaggerui")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/docs" || r.URL.Path == "/docs/" {
			serveSwaggerIndex(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveSwaggerIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>VinCommerce API — Documentation</title>
  <link rel="stylesheet" href="/docs/swagger-ui.css">
  <link rel="icon" href="/docs/favicon-32x32.png">
  <style>html { box-sizing: border-box; overflow-y: scroll; } body { margin: 0; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/docs/swagger-ui-bundle.js"></script>
  <script src="/docs/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: '/docs/openapi.json',
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
        layout: 'StandaloneLayout',
        persistAuthorization: true,
      })
    }
  </script>
</body>
</html>`))
}
