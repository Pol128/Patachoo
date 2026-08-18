package main

import (
	"strings"
	"testing"
)

func TestDocumentEstUnHTMLComplet(t *testing.T) {
	page := document("Patachoo", "Bonjour")

	for _, attendu := range []string{"<!doctype html>", `<html lang="fr">`, "<title>Patachoo</title>", "<p>Bonjour</p>", "</html>"} {
		if !strings.Contains(page, attendu) {
			t.Errorf("page sans %q :\n%s", attendu, page)
		}
	}
}

// Le jour où le titre viendra du nom d'une recette, il viendra d'un
// utilisateur. Autant que l'échappement soit couvert avant, pas après.
func TestDocumentEchappeSesEntrees(t *testing.T) {
	page := document(`<script>alert("xss")</script>`, `Chausson aux pommes & cannelle`)

	if strings.Contains(page, "<script>") {
		t.Errorf("balise script non échappée :\n%s", page)
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Errorf("titre attendu échappé :\n%s", page)
	}
	if !strings.Contains(page, "pommes &amp; cannelle") {
		t.Errorf("esperluette non échappée :\n%s", page)
	}
}
