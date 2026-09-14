package main

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"github.com/pocketbase/pocketbase/tools/security"
)

// nomCookieAntiRejeu porte la moitié que seul le navigateur légitime renvoie.
//
// Le préfixe __Host- n'est pas décoratif : il interdit au cookie de porter un
// Domain et exige Secure et Path=/, ce qui empêche un voisin same-site — un
// autre site du même domaine parent — de poser le cookie de la paire. Sans lui,
// celui-là pourrait fournir les deux moitiés à la fois, et la double soumission
// ne protégerait plus que du cross-site. Il ne coûte rien sur un cookie neuf ;
// c'est PATA-74 qui traite le même préfixe sur le cookie de session.
const nomCookieAntiRejeu = "__Host-patachoo_antirejeu"

// champAntiRejeu est le nom du champ caché que chaque formulaire en POST porte.
// Il commence par un souligné pour ne jamais entrer en collision avec un champ
// métier : aucune de nos collections n'en a qui commence ainsi.
const champAntiRejeu = "_antirejeu"

// cleJetonAntiRejeu range dans le magasin de la requête la valeur courante du
// jeton — celle du cookie reçu, ou celle que la réponse vient de poser.
//
// Un magasin plutôt qu'une relecture du cookie, comme cleSessionPorteeParLeCookie :
// une requête qui n'en portait pas de valide en reçoit un neuf, et c'est cette
// valeur-là que le gabarit doit recopier et que le contrôle doit attendre.
const cleJetonAntiRejeu = "patachooJetonAntiRejeu"

// longueurDuJetonAntiRejeu est le nombre de caractères tirés par
// security.RandomString, qui puise dans crypto/rand.
const longueurDuJetonAntiRejeu = 32

// cheminsSansJetonAntiRejeu énumère les racines où la pose ne tourne pas, par
// préfixe.
//
// Aucune de ces réponses n'a de champ caché à garnir : c'est le raisonnement
// que exigeLeJetonAntiRejeu tient déjà pour le contrôle — « rien ne sert de
// gabarit sous /api/ et /_/ » — et il vaut autant pour la pose. /statique/ s'y
// ajoute : la feuille de style et htmx ne sont pas davantage des formulaires.
//
// Ce qu'une pose sans garde coûterait, et qui n'est pas une gêne d'esthétique :
// ce sont exactement les chemins que cheminsSansCacheControl (entetes.go)
// laisse délibérément mettre en cache. Leur réponse transporterait le
// Set-Cookie du jeton, et un cache partagé réglé pour ignorer Set-Cookie sur
// les assets — proxy_ignore_headers Set-Cookie, recette nginx courante —
// rejouerait la même valeur à tous les visiteurs. L'attaquant n'aurait plus
// qu'à demander /statique/patachoo.css, lire le jeton dans l'en-tête et le
// recopier dans son champ caché : la comparaison passerait, et le constat que
// cette tâche ferme serait rouvert. Accessoirement, une sous-ressource
// cross-site n'emporte pas le cookie sous SameSite=Lax : là où le navigateur
// accepte encore les cookies tiers, la pose en tirerait un neuf et écraserait
// celui du navigateur, faisant tomber en 403 les formulaires déjà ouverts.
//
// Une liste à elle, et non celle de entetes.go dont elle reprend les valeurs :
// les deux décisions coïncident aujourd'hui sans se déduire l'une de l'autre —
// l'une dit ce qui reste en cache, l'autre ce qui ne sert aucun gabarit.
//
// La barre finale est portée par le préfixe, et non déduite, comme pour
// prefixesDePocketBase : sans elle, /apiculture serait tenu pour une route de
// l'API.
var cheminsSansJetonAntiRejeu = []string{"/statique/", "/api/", "/_/"}

// porteLeJetonAntiRejeu dit si le chemin demandé reçoit la pose.
//
// Par préfixe, et non route par route : une page ajoutée demain est couverte
// sans que personne n'y pense, alors qu'un oubli sur une route nouvelle la
// priverait de son champ caché sans que rien ne le dise.
//
// Un simple préfixe sur URL.Path suffit, sans normalisation à écrire : les
// middlewares du routeur ne s'exécutent qu'après l'appariement par le
// http.ServeMux, sur un chemin déjà nettoyé et déjà déséchappé.
func porteLeJetonAntiRejeu(chemin string) bool {
	for _, prefixe := range cheminsSansJetonAntiRejeu {
		if strings.HasPrefix(chemin, prefixe) {
			return false
		}
	}
	return true
}

// prioritePoseDuJeton fait passer la pose avant tout middleware de route.
//
// Les middlewares du routeur et ceux des routes sont fondus dans un même
// crochet trié par priorité croissante (tools/router/router.go) ; à égalité,
// c'est l'ordre de branchement qui tranche, et celui-là ne tient qu'à
// l'écriture de brancheLesRoutes. Une priorité strictement négative dit ce
// qu'on veut vraiment — le contrôle lit une valeur que la pose a déjà rangée —
// sans dépendre de cet ordre-là. Un cran, comme pour la session : plus l'écart
// est petit, moins il reste de place pour qu'un middleware tiers vienne s'y
// glisser.
const prioritePoseDuJeton = -1

// brancheLAntiRejeu pose sur le routeur le middleware qui tient le cookie.
//
// Sur le routeur pour la pose, parce que toute page portant un formulaire doit
// pouvoir recopier le jeton — et route par route pour le contrôle (voir
// exigeLeJetonAntiRejeu).
func brancheLAntiRejeu(routeur *router.Router[*core.RequestEvent]) {
	routeur.Bind(poseLeJetonAntiRejeu())
}

// poseLeJetonAntiRejeu garantit que la requête a un jeton, et le range.
//
// Le cookie reçu est gardé tel quel quand il y en a un : le faire tourner à
// chaque requête périmerait les champs cachés de tous les onglets déjà
// ouverts, pour un gain nul — sa valeur n'est pas un secret d'authentification,
// elle ne vaut que confrontée au cookie que seul le navigateur légitime envoie.
func poseLeJetonAntiRejeu() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooPoseLeJetonAntiRejeu",
		Priority: prioritePoseDuJeton,
		Func: func(e *core.RequestEvent) error {
			if !porteLeJetonAntiRejeu(e.Request.URL.Path) {
				return e.Next()
			}

			jeton := jetonPorteParLeCookie(e)
			if jeton == "" {
				jeton = security.RandomString(longueurDuJetonAntiRejeu)
				poseLeCookie(e, cookieAntiRejeu(jeton, dureeDuJetonAntiRejeu(e.App)))
			}
			e.Set(cleJetonAntiRejeu, jeton)
			return e.Next()
		},
	}
}

// jetonPorteParLeCookie lit la moitié que le navigateur a jointe, ou "".
func jetonPorteParLeCookie(e *core.RequestEvent) string {
	cookie, err := e.Request.Cookie(nomCookieAntiRejeu)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// jetonAntiRejeuCourant rend la valeur rangée par la pose, pour les gabarits.
func jetonAntiRejeuCourant(e *core.RequestEvent) string {
	jeton, _ := e.Get(cleJetonAntiRejeu).(string)
	return jeton
}

// exigeLeJetonAntiRejeu refuse un POST dont le champ caché ne reproduit pas le
// cookie : c'est la double soumission, et c'est ce qui ferme la porte qu'une
// page tierce empruntait.
//
// Posé route par route, et non sur le routeur : un middleware posé sur le
// routeur s'appliquerait aussi à /api/ et à /_/, où les clients portent leur
// propre jeton dans Authorization et où rien ne sert de gabarit — aucun d'eux
// n'aurait de champ caché à joindre.
//
// Avant exigeUneSession là où les deux se posent : une requête forgée par un
// autre site n'a pas à être distinguée selon qu'une session est ouverte ou non,
// et le refus ne dit donc rien de l'état du navigateur qui la subit.
func exigeLeJetonAntiRejeu() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "patachooExigeLeJetonAntiRejeu",
		Func: func(e *core.RequestEvent) error {
			// valeursSoumises traite les deux encodages — le formulaire de
			// recette poste en multipart —, et ce qu'elle a lu reste disponible
			// au gestionnaire, FormFile("image") compris.
			champs, err := valeursSoumises(e)
			if err != nil {
				// Un corps illisible n'est pas un refus de jeton : le rendre
				// comme tel masquerait une borne de taille franchie derrière
				// une page qui invite à recommencer.
				return err
			}

			attendu := jetonAntiRejeuCourant(e)
			soumis := champs.Get(champAntiRejeu)
			if attendu == "" || subtle.ConstantTimeCompare([]byte(attendu), []byte(soumis)) != 1 {
				return refuseLeJetonAntiRejeu(e)
			}
			return e.Next()
		},
	}
}

// refuseLeJetonAntiRejeu rend la page de refus sous un 403.
//
// En HTML et non par apis.NewForbiddenError : celui-ci ressort en JSON sous
// router.ErrorHandler, et l'utilisateur d'un formulaire y verrait du JSON brut
// à la place de sa page — c'est exactement ce que rendLeDepassementEnHTML évite
// déjà pour le 429.
func refuseLeJetonAntiRejeu(e *core.RequestEvent) error {
	return rendreAvecStatut(e, http.StatusForbidden, "refus-antirejeu.html", "refus-antirejeu-corps.html", &donneesPage{
		Titre: "Formulaire expiré — Patachoo",
	})
}

// cookieAntiRejeu porte la moitié que le navigateur garde.
//
// HttpOnly tient parce que c'est le serveur qui recopie la valeur dans le
// gabarit : aucun JavaScript n'a à la lire. Secure et Path=/ sont exigés par le
// préfixe __Host-, qui interdit aussi le Domain — absent ici, et il doit le
// rester.
//
// Max-Age suit la durée du jeton d'authentification, comme cookieDeSession :
// un cookie qui survit à la session qu'il accompagne ne protège plus rien
// d'utile, et l'inverse ferait refuser un formulaire encore ouvert.
func cookieAntiRejeu(jeton string, duree time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     nomCookieAntiRejeu,
		Value:    jeton,
		Path:     "/",
		MaxAge:   int(duree.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// dureeDuJetonAntiRejeu lit sur users la durée que cookieDeSession lit sur le
// compte connecté — lequel n'existe pas encore quand la page de connexion se
// rend, alors que c'est justement l'une de celles qui portent un formulaire.
//
// Une collection illisible rend zéro, soit un cookie de session de navigateur :
// il meurt à la fermeture du navigateur au lieu de vivre trop longtemps, et la
// paire tient tout de même le temps de la visite.
func dureeDuJetonAntiRejeu(app core.App) time.Duration {
	collection, err := app.FindCachedCollectionByNameOrId("users")
	if err != nil {
		app.Logger().Error("durée du jeton d'authentification illisible : cookie anti-rejeu sans Max-Age", "erreur", err)
		return 0
	}
	return collection.AuthToken.DurationTime()
}
