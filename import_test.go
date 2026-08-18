package main

import "testing"

func TestAFaireNommeLaTache(t *testing.T) {
	rep := aFaire("import par URL", "PATA-9")

	if rep["tache"] != "PATA-9" {
		t.Errorf("tache = %q, attendu PATA-9", rep["tache"])
	}
	// Le message doit dire ce qui manque, pas « une erreur est survenue ».
	if rep["message"] == "" || rep["message"] == "erreur" {
		t.Errorf("message inutilisable : %q", rep["message"])
	}
}
