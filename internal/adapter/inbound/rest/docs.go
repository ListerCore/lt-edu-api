package rest

// SwaggerUI is the page served at /api/docs: Swagger UI from its CDN, reading the embedded OpenAPI document
const SwaggerUI = `<!doctype html>
<html lang="es">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ListerCore Edu API · Swagger</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.ui = SwaggerUIBundle({ url: "/api/docs/openapi.json", dom_id: "#swagger-ui", persistAuthorization: true });
  </script>
</body>
</html>`
