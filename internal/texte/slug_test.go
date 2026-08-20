package texte

import "testing"

func TestSlug(t *testing.T) {
	cas := map[string]string{
		"Entrée":           "entree",
		"Petit-déjeuner":   "petit-dejeuner",
		"Goûter":           "gouter",
		"Plat  principal":  "plat-principal",
		"  Végétarien ":    "vegetarien",
		"Cœur d'artichaut": "coeur-d-artichaut",
		"":                 "",
	}
	for entree, attendu := range cas {
		if obtenu := Slug(entree); obtenu != attendu {
			t.Errorf("Slug(%q) = %q, attendu %q", entree, obtenu, attendu)
		}
	}
}
