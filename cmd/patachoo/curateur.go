package main

import (
	"net/http"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// champCurateur porte le droit d'entrer dans l'établi — le premier privilège du
// produit, et le seul jusqu'ici.
//
// Il est posé sur users par migrations/1789920900_droit_curateur.go, et non sur
// _superusers, parce qu'un superutilisateur n'a pas de session sur le site :
// /connexion n'émet que des jetons users (session.go). Une garde qui exigerait
// IsSuperuser() livrerait une page que personne ne peut voir.
const champCurateur = "curator"

// exigeUnCurateur garde les routes réservées aux curateurs.
//
// Le patron exact de exigeUneSession() (recettes.go) : un Id préfixé patachoo,
// et un handler qu'on pose par .Bind(…). Aucune route de production ne le porte
// encore — les pages de l'établi sont PATA-125 à PATA-127, et c'est là qu'il se
// branchera, route par route.
//
// L'ordre des deux contrôles n'est pas indifférent. Le visiteur est renvoyé se
// connecter, comme partout ailleurs sur le site : un curateur dont la session a
// expiré doit pouvoir se reconnecter, pas se cogner à un refus. Le compte
// connecté sans le droit, lui, reçoit un 403.
//
// Un 403 et non un 404, tranché par Paul le 20/09/2026. L'établi diverge donc
// volontairement de pageInscription (inscription.go), qui répond 404 quand
// l'inscription est fermée — et c'est écrit ici pour qu'on ne relise pas cet
// écart comme un oubli. Les deux ne protègent pas la même chose : une
// inscription fermée cache une porte à des inconnus, qui n'ont aucune raison
// d'apprendre qu'elle existe ; l'établi oppose son refus à un compte déjà connu
// de l'instance, sur un produit auto-hébergé entre proches, et « cette page
// existe, mais pas pour toi » y est plus honnête — et bien plus simple à
// déboguer le jour où le droit a été oublié sur un compte. À reprendre si
// l'instance s'ouvre un jour à l'inscription publique.
//
// Le refus est celui de apis, donc du JSON, et non une page : cette tâche ne
// livre aucun gabarit. Son corps reste sobre, il ne nomme ni la garde ni le
// champ. C'est aux pages de l'établi de l'habiller en HTML si elles le veulent,
// comme refuseLeJetonAntiRejeu le fait déjà pour son propre 403.
//
// La garde lit e.Auth et rien d'autre : c'est l'enregistrement users relu en
// base à chaque requête par la chaîne d'authentification, quand le jeton, lui,
// ne porte pas le champ. Aucune valeur recopiée en session, aucun cache — c'est
// ce qui fait qu'un droit retiré vaut dès la requête suivante, sans attendre
// une reconnexion.
func exigeUnCurateur() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "patachooExigeUnCurateur",
		Func: func(e *core.RequestEvent) error {
			if e.Auth == nil {
				return e.Redirect(http.StatusSeeOther, "/connexion")
			}
			if !e.Auth.GetBool(champCurateur) {
				return apis.NewForbiddenError("", nil)
			}
			return e.Next()
		},
	}
}

// figeLeCurateurSaufPourLeSuperuser bouche le trou évident d'un droit porté par
// la collection des comptes : users.UpdateRule vaut « id = @request.auth.id »,
// donc un compte connecté modifie bel et bien son propre enregistrement par
// PATCH /api/collections/users/records/{id} — et s'y cocherait le droit
// lui-même. C'est fige (acces.go), pour la raison qu'il énonce : une règle de
// collection n'est évaluée qu'en allant chercher la ligne, donc sur son état
// d'avant modification, et elle a déjà dit oui quand la valeur change.
//
// Avec une exception, et elle est la seule porte du droit : l'octroi passe par
// /_/, c'est-à-dire par cette même route sous un jeton de superuser. Un gel
// sans exception — la forme que fige prend sur recipes, comments et ingredients
// — livrerait une garde que personne ne peut franchir, faute de pouvoir
// accorder quoi que ce soit. Les trois champs figés ailleurs n'ont pas ce
// problème : ils se posent à la création, celui-ci ne se pose qu'après.
//
// Le superuser est reconnu comme PocketBase le fait lui-même pour décider des
// règles de la requête (requestInfo.HasSuperuserAuth) : l'enregistrement
// authentifié, et sa collection.
func figeLeCurateurSaufPourLeSuperuser(e *core.RecordRequestEvent) error {
	if e.Auth != nil && e.Auth.IsSuperuser() {
		return e.Next()
	}
	return fige(champCurateur)(e)
}
