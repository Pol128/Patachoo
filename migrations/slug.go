package migrations

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var ligatures = strings.NewReplacer(
	"œ", "oe", "Œ", "OE",
	"æ", "ae", "Æ", "AE",
	"ß", "ss",
)

// slug rend la forme d'une valeur qui sert de clé : minuscules, sans accent,
// mots joints par un tiret.
//
// Sans ça « Petit-déjeuner » et « petit dejeuner » seraient deux entrées
// distinctes, et l'index d'unicité ne les rapprocherait jamais.
func slug(texte string) string {
	// Les ligatures ne se décomposent pas : NFD laisse « œ » entier, et un
	// « Cœur d'artichaut » ressortait « c-ur-d-artichaut ». Elles se remplacent
	// donc à la main, avant la décomposition.
	texte = ligatures.Replace(texte)

	sansAccents, _, err := transform.String(
		transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC),
		texte,
	)
	if err != nil {
		sansAccents = texte
	}

	var b strings.Builder
	tiretEnAttente := false
	for _, r := range strings.ToLower(sansAccents) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if tiretEnAttente && b.Len() > 0 {
				b.WriteByte('-')
			}
			tiretEnAttente = false
			b.WriteRune(r)
		default:
			tiretEnAttente = true
		}
	}
	return b.String()
}
