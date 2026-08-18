package main

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// pageAccueil sert la page d'accueil.
//
// Le squelette répond avec un document écrit à la main : poser les gabarits
// html/template et HTMX est le sujet de PATA-12. Ce que cette route prouve,
// c'est que nos routes cohabitent avec celles de PocketBase.
func pageAccueil(e *core.RequestEvent) error {
	return e.HTML(http.StatusOK, document("Patachoo", "Le carnet de recettes est en construction."))
}

// document assemble un document HTML minimal mais valide.
//
// Le titre et le corps sont échappés : ils viendront un jour de données, et
// une page qui n'échappe pas ses entrées est une injection en attente.
func document(titre, corps string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>` + "\n")
	b.WriteString(`<html lang="fr">` + "\n")
	b.WriteString(`<head>` + "\n")
	b.WriteString(`<meta charset="utf-8">` + "\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	b.WriteString(`<title>` + template.HTMLEscapeString(titre) + `</title>` + "\n")
	b.WriteString(`</head>` + "\n")
	b.WriteString(`<body>` + "\n")
	b.WriteString(`<h1>` + template.HTMLEscapeString(titre) + `</h1>` + "\n")
	b.WriteString(`<p>` + template.HTMLEscapeString(corps) + `</p>` + "\n")
	b.WriteString(`</body>` + "\n")
	b.WriteString(`</html>` + "\n")
	return b.String()
}
