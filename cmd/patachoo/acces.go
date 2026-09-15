package main

import "github.com/pocketbase/pocketbase/core"

// champAuteur porte le compte qui a ajouté la recette. Rien ne le remplit tout
// seul : ce n'est pas un champ système de PocketBase, c'est le nôtre, et la
// règle de suppression s'appuie dessus.
const champAuteur = "created_by"

// brancheLAcces accroche les hooks qui tiennent ce que l'API REST ne doit pas
// laisser réécrire : l'auteur d'une recette, la signature et le rattachement
// d'une note.
//
// Des hooks de requête, et non de modèle : un core.RecordEvent porte
// l'application et l'enregistrement, mais ni requête ni utilisateur
// authentifié — OnRecordCreate ne saurait donc pas de qui remplir le champ.
func brancheLAcces(app core.App) {
	app.OnRecordCreateRequest("recipes").BindFunc(attribueALAppelant)
	app.OnRecordUpdateRequest("recipes").BindFunc(fige(champAuteur))
	// Le même patron sur comments, et non une condition ajoutée à UpdateRule :
	// une règle ne dit rien du cas où le champ n'est pas fourni, et les deux
	// collections gagnent à ne se protéger que d'une seule façon.
	app.OnRecordUpdateRequest("comments").BindFunc(fige("author", "recipe"))
}

// attribueALAppelant pose l'auteur d'une recette créée par l'API.
func attribueALAppelant(e *core.RecordRequestEvent) error {
	poseLAuteur(e.Record, e.Auth)
	return e.Next()
}

// poseLAuteur attribue l'enregistrement au compte donné, sans regarder ce
// qu'il portait déjà : un champ qu'on ne remplirait que s'il est vide reste
// falsifiable — il suffit de l'envoyer rempli.
//
// Elle est appelable hors requête, parce qu'une recette créée par notre propre
// code passe par app.Save() et ne déclenche donc aucun hook de requête : c'est
// alors à l'appelant de poser l'auteur depuis l'utilisateur de la session.
func poseLAuteur(enregistrement *core.Record, compte *core.Record) {
	// Un superuser n'est pas un compte de users : son identifiant ferait une
	// relation vers un enregistrement inexistant, et l'administration ne
	// pourrait plus créer la moindre recette. C'est aussi le seul endroit d'où
	// une recette se réattribue à la main.
	if compte != nil && compte.IsSuperuser() {
		return
	}

	id := ""
	if compte != nil {
		id = compte.Id
	}
	enregistrement.Set(champAuteur, id)
}

// fige rend un hook de modification qui remet les valeurs enregistrées, quoi
// que la requête propose.
//
// Pas un ornement, et pas un doublon des règles de collection : celles-ci ne
// sont évaluées qu'en allant chercher la ligne, donc sur son état d'avant
// modification. Une requête qui retourne un champ du seul enregistrement
// qu'elle a le droit de modifier passe donc le contrôle, et c'est ici qu'elle
// est rattrapée — sur recipes, un compte s'attribuerait la recette d'un autre
// puis la supprimerait ; sur comments, il signerait son texte du nom d'un
// autre compte, ou déplacerait sa note sous un plat qu'il n'a pas cuisiné.
//
// Sans exception, superuser compris. La création en a une, par nécessité :
// l'identifiant d'un superuser ne désigne aucun compte de users, et la
// relation serait refusée. Ici il n'y a rien à contourner — réécrire une
// valeur déjà enregistrée ne peut pas échouer. Une recette s'attribue à sa
// création, et là seulement.
func fige(champs ...string) func(*core.RecordRequestEvent) error {
	return func(e *core.RecordRequestEvent) error {
		for _, champ := range champs {
			e.Record.Set(champ, e.Record.Original().GetString(champ))
		}
		return e.Next()
	}
}

// sienne dit si la recette appartient au compte donné.
//
// Le premier terme n'est pas décoratif, et c'est celui de DeleteRule :
// created_by n'est pas Required, et une recette peut le porter vide. Sans lui,
// une recette sans auteur appartiendrait à quiconque n'en a pas non plus.
//
// La règle de collection est transposée ici parce que e.App.Delete ne
// l'applique pas : les règles gardent l'API REST, pas notre code. Elle reste
// en place, celle-ci la redouble.
func sienne(recette *core.Record, compte string) bool {
	auteur := recette.GetString(champAuteur)
	return auteur != "" && auteur == compte
}
