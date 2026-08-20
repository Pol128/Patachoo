package main

import "github.com/pocketbase/pocketbase/core"

// champAuteur porte le compte qui a ajouté la recette. Rien ne le remplit tout
// seul : ce n'est pas un champ système de PocketBase, c'est le nôtre, et la
// règle de suppression s'appuie dessus.
const champAuteur = "created_by"

// brancheLAcces accroche à recipes les deux hooks qui tiennent created_by.
//
// Des hooks de requête, et non de modèle : un core.RecordEvent porte
// l'application et l'enregistrement, mais ni requête ni utilisateur
// authentifié — OnRecordCreate ne saurait donc pas de qui remplir le champ.
func brancheLAcces(app core.App) {
	app.OnRecordCreateRequest("recipes").BindFunc(attribueALAppelant)
	app.OnRecordUpdateRequest("recipes").BindFunc(restaureLAuteur)
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

// restaureLAuteur remet la valeur enregistrée, quoi que la requête propose.
//
// Symétrique de la création, et pas un ornement : tout compte connecté a le
// droit de modifier n'importe quelle recette. Sans ce hook, il s'attribue
// celle d'un autre, puis la supprime — et la règle de suppression ne protège
// plus rien.
//
// Sans exception, superuser compris. La création en a une, par nécessité :
// l'identifiant d'un superuser ne désigne aucun compte de users, et la
// relation serait refusée. Ici il n'y a rien à contourner — réécrire une
// valeur déjà enregistrée ne peut pas échouer. Une recette s'attribue à sa
// création, et là seulement.
func restaureLAuteur(e *core.RecordRequestEvent) error {
	e.Record.Set(champAuteur, e.Record.Original().GetString(champAuteur))
	return e.Next()
}
