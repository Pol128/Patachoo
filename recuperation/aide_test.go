package recuperation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// Outillage commun aux tests du paquet. Trois principes le gouvernent, et ils
// viennent des critères de la tâche :
//
//   - aucun test ne sort sur le réseau : les serveurs sont des httptest, et la
//     résolution de noms est injectée ;
//   - les serveurs de test écoutent sur la boucle locale, que la politique
//     d'IP refuse par construction — d'où `autorise`, qui lève l'interdiction
//     pour ces adresses-là et pour elles seules. Tout le reste, y compris une
//     autre adresse de boucle locale, reste refusé : c'est ce qui permet de
//     tester une redirection vers 127.0.0.1 depuis un serveur qui y vit ;
//   - la cause se lit par errors.As, jamais dans le message.

// echec rend l'erreur du paquet portée par err. C'est le seul chemin par lequel
// les tests lisent une cause : chercher une sous-chaîne dans Error() ferait
// passer un test qui ne prouve rien.
func echec(t *testing.T, err error) *Erreur {
	t.Helper()
	if err == nil {
		t.Fatal("succès inattendu : une cause d'échec était attendue")
	}
	var e *Erreur
	if !errors.As(err, &e) {
		t.Fatalf("erreur %v (%T) : ce n'est pas une *Erreur du paquet", err, err)
	}
	return e
}

// serveur monte un serveur de test dont le robots.txt répond 404 — donc qui
// n'interdit rien — et qui délègue le reste à h.
func serveur(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	return serveurRobots(t, "", h)
}

// serveurRobots monte un serveur de test qui sert robots comme /robots.txt, ou
// répond 404 si robots est vide.
func serveurRobots(t *testing.T, robots string, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			if robots == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, robots)
			return
		}
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// autorise lève la politique d'IP pour les adresses exactes des serveurs
// donnés, et pour elles seules.
func autorise(serveurs ...*httptest.Server) Option {
	permises := make(map[string]bool, len(serveurs))
	for _, s := range serveurs {
		permises[s.Listener.Addr().String()] = true
	}
	return AvecExceptionDePolitique(func(ap netip.AddrPort) bool { return permises[ap.String()] })
}

// avecResolution remplace la résolution de noms par une table. Un nom absent de
// la table est injoignable : aucun test ne peut donc interroger le DNS sans
// qu'on l'ait voulu.
func avecResolution(table map[string]string) Option {
	return AvecResolution(func(_ context.Context, hote string) ([]netip.Addr, error) {
		brut, connu := table[hote]
		if !connu {
			return nil, fmt.Errorf("nom hors de la résolution injectée : %s", hote)
		}
		return []netip.Addr{netip.MustParseAddr(brut)}, nil
	})
}

// transportPiege fait échouer le test à la première requête sortante.
type transportPiege struct{ t *testing.T }

func (p transportPiege) RoundTrip(r *http.Request) (*http.Response, error) {
	p.t.Errorf("requête sortante vers %s alors qu'aucune ne devait partir", r.URL)
	return nil, errors.New("piège")
}

// recupere appelle le paquet avec un contexte neutre.
func recupere(t *testing.T, adresse string, choix ...Option) (Page, error) {
	t.Helper()
	return Recupere(context.Background(), adresse, choix...)
}
