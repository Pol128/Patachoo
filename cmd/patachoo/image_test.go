package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Pol128/Patachoo/recuperation"
	"github.com/pocketbase/pocketbase/core"
)

// L'image d'une recette importée est téléchargée par le serveur au moment où
// l'utilisateur valide le formulaire, et rangée dans le champ fichier de la
// collection. Son URL traverse le formulaire dans un champ caché : elle est
// donc postée par le client, et se traite comme l'URL d'import — par le
// récupérateur de PATA-8, politique d'adresses comprise.
//
// Les tests de ce fichier ne sortent jamais sur le réseau. Ils montent des
// serveurs httptest, qui écoutent sur la boucle locale que la politique refuse
// par construction, et lèvent l'interdiction pour ces adresses-là et pour
// elles seules. Ce qu'ils règlent est le récupérateur réel, jamais un faux :
// un faux passerait encore le jour où l'image cesserait de passer par lui.

// --- Montage ---------------------------------------------------------------

// avecTelechargement règle le récupérateur pour la durée du test.
func avecTelechargement(t *testing.T, choix ...recuperation.Option) {
	t.Helper()

	precedent := optionsDuTelechargement
	optionsDuTelechargement = choix
	t.Cleanup(func() { optionsDuTelechargement = precedent })
}

// serveurDImages monte un serveur de test dont le robots.txt répond 404 —
// donc qui n'interdit rien — et qui délègue le reste à h.
func serveurDImages(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	return serveurDImagesRobots(t, "", h)
}

// serveurDImagesRobots monte un serveur de test qui sert robots comme
// /robots.txt, ou répond 404 si robots est vide.
func serveurDImagesRobots(t *testing.T, robots string, h http.HandlerFunc) *httptest.Server {
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

// sertLesOctets rend toujours le même corps, sous le type annoncé — que le
// type mente ou non : c'est le contenu qui décide, pas l'en-tête.
func sertLesOctets(corps []byte, typeContenu string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", typeContenu)
		_, _ = w.Write(corps)
	}
}

// autoriseLesServeurs lève la politique d'adresses pour les adresses exactes
// des serveurs donnés, et pour elles seules.
func autoriseLesServeurs(serveurs ...*httptest.Server) recuperation.Option {
	adresses := make([]string, 0, len(serveurs))
	for _, s := range serveurs {
		adresses = append(adresses, s.Listener.Addr().String())
	}
	return autoriseLesAdresses(adresses...)
}

// autoriseLesAdresses lève la politique pour ces couples adresse:port, et pour
// eux seuls : tout le reste de la boucle locale reste refusé, ce qui permet de
// tester une redirection vers 127.0.0.1 depuis un serveur qui y vit.
func autoriseLesAdresses(adresses ...string) recuperation.Option {
	permises := make(map[string]bool, len(adresses))
	for _, a := range adresses {
		permises[a] = true
	}
	return recuperation.AvecExceptionDePolitique(func(ap netip.AddrPort) bool {
		return permises[ap.String()]
	})
}

// resolutionDeTest traduit les noms de la table. Un nom absent est
// injoignable : aucun test ne peut interroger le DNS de la machine.
func resolutionDeTest(table map[string]string) recuperation.Option {
	return recuperation.AvecResolution(func(_ context.Context, hote string) ([]netip.Addr, error) {
		brut, connu := table[hote]
		if !connu {
			return nil, fmt.Errorf("nom hors de la résolution injectée : %s", hote)
		}
		return []netip.Addr{netip.MustParseAddr(brut)}, nil
	})
}

// aucuneRequeteSortante piège le transport : le test échoue si la moindre
// requête part. C'est ce qui distingue « refusé » de « refusé après coup ».
func aucuneRequeteSortante(t *testing.T) {
	t.Helper()
	avecTelechargement(t, recuperation.AvecTransport(transportInterdit{t}))
}

type transportInterdit struct{ t *testing.T }

func (p transportInterdit) RoundTrip(r *http.Request) (*http.Response, error) {
	p.t.Errorf("requête sortante vers %s alors qu'aucune ne devait partir", r.URL)
	return nil, errors.New("piège")
}

// avecPixelsMax abaisse le garde-fou des dimensions pour la durée du test :
// fabriquer une image de quarante mégapixels coûterait plus cher que ce que le
// test prouve.
func avecPixelsMax(t *testing.T, seuil int64) {
	t.Helper()

	precedent := pixelsMax
	pixelsMax = seuil
	t.Cleanup(func() { pixelsMax = precedent })
}

// --- Lecture du stockage ---------------------------------------------------

// fichierStocke rend les octets du fichier attaché à la recette.
func fichierStocke(t *testing.T, app core.App, recette *core.Record) []byte {
	t.Helper()

	nom := recette.GetString("image")
	if nom == "" {
		t.Fatal("aucune image attachée à la recette")
	}

	fsys, err := app.NewFilesystem()
	if err != nil {
		t.Fatalf("stockage : %v", err)
	}
	defer fsys.Close()

	lecteur, err := fsys.GetReader(recette.BaseFilesPath() + "/" + nom)
	if err != nil {
		t.Fatalf("lecture du fichier %q : %v", nom, err)
	}
	defer lecteur.Close()

	var tampon bytes.Buffer
	if _, err := tampon.ReadFrom(lecteur); err != nil {
		t.Fatalf("lecture du fichier %q : %v", nom, err)
	}
	return tampon.Bytes()
}

// exigeStockageVide dit qu'aucun fichier n'a été écrit : une image refusée ne
// doit pas laisser d'orphelin derrière elle.
func exigeStockageVide(t *testing.T, app core.App) {
	t.Helper()

	fsys, err := app.NewFilesystem()
	if err != nil {
		t.Fatalf("stockage : %v", err)
	}
	defer fsys.Close()

	objets, err := fsys.List("")
	if err != nil {
		t.Fatalf("inventaire du stockage : %v", err)
	}
	for _, objet := range objets {
		t.Errorf("fichier écrit dans le stockage alors qu'aucun n'était attendu : %s", objet.Key)
	}
}

// miniatureDe fabrique la miniature de la grille à partir du fichier stocké,
// et rend ce que ses octets disent de ses dimensions.
func miniatureDe(t *testing.T, app core.App, recette *core.Record, taille string) image.Config {
	t.Helper()

	fsys, err := app.NewFilesystem()
	if err != nil {
		t.Fatalf("stockage : %v", err)
	}
	defer fsys.Close()

	origine := recette.BaseFilesPath() + "/" + recette.GetString("image")
	vignette := origine + "_" + taille
	if err := fsys.CreateThumb(origine, vignette, taille); err != nil {
		t.Fatalf("fabrication de la miniature %s : %v", taille, err)
	}

	lecteur, err := fsys.GetReader(vignette)
	if err != nil {
		t.Fatalf("lecture de la miniature : %v", err)
	}
	defer lecteur.Close()

	config, _, err := image.DecodeConfig(lecteur)
	if err != nil {
		t.Fatalf("décodage de la miniature : %v", err)
	}
	return config
}

// --- Images de test --------------------------------------------------------

// pngDeTaille rend un PNG réel des dimensions demandées : le garde-fou se
// juge sur ce que l'en-tête annonce, pas sur le poids du fichier.
func pngDeTaille(t *testing.T, largeur, hauteur int) []byte {
	t.Helper()

	corps := &bytes.Buffer{}
	if err := png.Encode(corps, image.NewRGBA(image.Rect(0, 0, largeur, hauteur))); err != nil {
		t.Fatalf("encodage du PNG %dx%d : %v", largeur, hauteur, err)
	}
	return corps.Bytes()
}

// jpegDeTest rend un JPEG réel.
func jpegDeTest(t *testing.T) []byte {
	t.Helper()

	corps := &bytes.Buffer{}
	if err := jpeg.Encode(corps, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatalf("encodage du JPEG de test : %v", err)
	}
	return corps.Bytes()
}

// webpDeTest rend un WebP sans perte de 1×1, écrit en dur : x/image sait
// décoder le WebP, personne ne sait l'encoder en Go.
func webpDeTest(t *testing.T) []byte {
	t.Helper()

	octets, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatalf("WebP de test : %v", err)
	}
	return octets
}

// gifDeTest rend un GIF réel : refusé, mais pour la bonne raison — il se
// décode, et son format n'est pas dans la liste.
func gifDeTest(t *testing.T) []byte {
	t.Helper()

	corps := &bytes.Buffer{}
	if err := gif.Encode(corps, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatalf("encodage du GIF de test : %v", err)
	}
	return corps.Bytes()
}

// avifDeTest rend l'en-tête d'un AVIF réel — la boîte ftyp, marques de
// compatibilité comprises.
//
// Le schéma accepte image/avif au téléversement manuel, et c'est voulu : un
// fichier choisi par l'utilisateur est son affaire. Ce que ce fixture sert à
// prouver est l'inverse, côté téléchargement : la bibliothèque de miniatures
// de PocketBase ne décode pas l'AVIF, et un AVIF stocké serait servi en pleine
// résolution dans la grille de vignettes.
func avifDeTest() []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x1c}, "ftypavif\x00\x00\x00\x00avifmif1miaf"...)
}

func svgDeTest() []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8"/></svg>`)
}

func pdfDeTest() []byte {
	return []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< >>\n%%EOF\n")
}

// imagesAcceptees rend les trois types que le téléchargement attache, avec le
// type de contenu sous lequel un site les servirait.
func imagesAcceptees(t *testing.T) map[string]struct {
	corps       []byte
	typeContenu string
	extension   string
} {
	t.Helper()

	return map[string]struct {
		corps       []byte
		typeContenu string
		extension   string
	}{
		"png":  {pngDeTest(t), "image/png", ".png"},
		"jpeg": {jpegDeTest(t), "image/jpeg", ".jpg"},
		"webp": {webpDeTest(t), "image/webp", ".webp"},
	}
}

// --- Le champ caché --------------------------------------------------------

// Déroulé n° 1 : l'aperçu d'import poste l'URL de l'image trouvée, pour que
// l'enregistrement sache quoi aller chercher. Sans ce champ, l'image de
// l'aperçu est perdue à la validation.
func TestLApercuDImportPosteLURLDeLImage(t *testing.T) {
	const attendue = "https://fourneaux-de-perlimpinpin.example/images/veloute-panais.jpg"

	_, mux, cookie := carnetDeTest(t)
	sert(t, pageDuCorpus(t, "image-objet"), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if trouvee := valeurEventuelleDe(corps, "image_url"); trouvee != attendue {
		t.Errorf("champ caché image_url %q, attendue %q", trouvee, attendue)
	}
}

// Le champ est vide sur une saisie manuelle : rien à télécharger, donc rien à
// poster.
func TestUneSaisieManuelleNePortePasDeChampImageURL(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	rec := demande(mux, "/recettes/nouvelle", cookie, nil)

	exigeSansAucun(t, rec.Body.String(), `name="image_url"`)
}

// --- Le succès -------------------------------------------------------------

// Critère 4 : les trois types acceptés sont téléchargés et attachés, tels que
// le site les sert — ni conversion, ni ré-encodage.
func TestUneImageDistanteEstTelechargeeEtAttachee(t *testing.T) {
	for nom, cas := range imagesAcceptees(t) {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			serveur := serveurDImages(t, sertLesOctets(cas.corps, cas.typeContenu))
			avecTelechargement(t, autoriseLesServeurs(serveur))

			champs := champsValides()
			champs.Set("image_url", serveur.URL+"/photo"+cas.extension)
			rec := poste(t, mux, "/recettes", cookie, champs)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
			}
			recette := laRecette(t, app)
			if stocke := fichierStocke(t, app, recette); !bytes.Equal(stocke, cas.corps) {
				t.Errorf("%d octets stockés, les %d octets servis attendus", len(stocke), len(cas.corps))
			}
		})
	}
}

// Critère 5 : la grille de vignettes sert la miniature 300x200, jamais
// l'original. Un format que la fabrication ne sait pas décoder retomberait sur
// l'original en pleine résolution — c'est ce test qui l'interdit.
func TestLImageTelechargeeEstMiniaturisable(t *testing.T) {
	for nom, cas := range imagesAcceptees(t) {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			serveur := serveurDImages(t, sertLesOctets(cas.corps, cas.typeContenu))
			avecTelechargement(t, autoriseLesServeurs(serveur))

			champs := champsValides()
			champs.Set("image_url", serveur.URL+"/photo"+cas.extension)
			poste(t, mux, "/recettes", cookie, champs)

			config := miniatureDe(t, app, laRecette(t, app), "300x200")
			if config.Width != 300 || config.Height != 200 {
				t.Errorf("miniature %dx%d, 300x200 attendue", config.Width, config.Height)
			}
		})
	}
}

// --- Le type jugé sur le contenu -------------------------------------------

// Critère 6 : PocketBase ne renifle le contenu pour nommer le fichier que si
// le nom qu'on lui passe n'a pas d'extension exploitable. Aucun morceau de
// l'URL distante ne devient donc un nom de fichier — sans quoi un PNG servi
// sous « .jpg » serait rangé sous une extension fausse.
func TestLeNomStockeVientDuContenuEtNonDeLURL(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	serveur := serveurDImages(t, sertLesOctets(pngDeTest(t), "image/jpeg"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/photo.jpg")
	poste(t, mux, "/recettes", cookie, champs)

	nom := laRecette(t, app).GetString("image")
	if !strings.HasSuffix(nom, ".png") {
		t.Errorf("fichier stocké sous %q, une extension .png attendue — le contenu est un PNG", nom)
	}
	if strings.Contains(nom, "photo") {
		t.Errorf("le nom stocké %q reprend un morceau de l'URL distante", nom)
	}
}

// Critère 6, l'autre moitié : une URL d'apparence honnête qui sert autre
// chose n'attache rien, et ne fait pas perdre l'import pour autant.
func TestUneURLDImageQuiSertDuHTMLNAttacheRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	serveur := serveurDImages(t, sertLesOctets([]byte("<html><body>pas une image</body></html>"), "image/jpeg"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/photo.jpg")
	rec := poste(t, mux, "/recettes", cookie, champs)

	exigeRecetteSansImage(t, app, rec)
}

// --- Les types refusés -----------------------------------------------------

// Critère 7 : trois types acceptés, et trois seulement. L'AVIF est refusé
// alors que le schéma l'accepte, et c'est l'arbitrage n° 1 : la fabrication de
// miniatures de PocketBase ne sait pas le décoder.
func TestUnTypeDImageRefuseNAttacheRien(t *testing.T) {
	cas := map[string]struct {
		corps       []byte
		typeContenu string
	}{
		"avif": {avifDeTest(), "image/avif"},
		"svg":  {svgDeTest(), "image/svg+xml"},
		"gif":  {gifDeTest(t), "image/gif"},
		"pdf":  {pdfDeTest(), "application/pdf"},
	}

	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			serveur := serveurDImages(t, sertLesOctets(c.corps, c.typeContenu))
			avecTelechargement(t, autoriseLesServeurs(serveur))

			champs := champsValides()
			champs.Set("image_url", serveur.URL+"/photo."+nom)
			rec := poste(t, mux, "/recettes", cookie, champs)

			exigeRecetteSansImage(t, app, rec)
			exigeStockageVide(t, app)
		})
	}
}

// --- Le garde-fou des dimensions -------------------------------------------

// Critère 8, arbitrage n° 4 : le plafond de cinq mébioctets borne les octets
// transférés, pas la mémoire du décodage. Un PNG uni compresse énormément, et
// c'est PocketBase qui le décodera plus tard, à la première miniature demandée.
func TestUneImageTropGrandeNEstPasAttachee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	avecPixelsMax(t, 4)
	serveur := serveurDImages(t, sertLesOctets(pngDeTaille(t, 3, 3), "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/enorme.png")
	rec := poste(t, mux, "/recettes", cookie, champs)

	exigeRecetteSansImage(t, app, rec)
	exigeStockageVide(t, app)
}

// L'autre côté du seuil : une image qui l'atteint sans le dépasser est
// attachée. Sans ce test, un garde-fou qui refuserait tout passerait.
func TestUneImageAuSeuilExactEstAttachee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	avecPixelsMax(t, 4)
	serveur := serveurDImages(t, sertLesOctets(pngDeTaille(t, 2, 2), "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/juste.png")
	poste(t, mux, "/recettes", cookie, champs)

	if laRecette(t, app).GetString("image") == "" {
		t.Error("aucune image attachée alors que ses dimensions atteignent tout juste le seuil")
	}
}

// --- Les causes d'échec de PATA-8 ------------------------------------------

// Critère 9 : chacune des causes que le récupérateur sait nommer laisse la
// recette créée, sans image. Perdre l'import parce que le site n'a pas servi
// son illustration serait le pire des échanges.
func TestChaqueEchecDeRecuperationLaisseLaRecetteSansImage(t *testing.T) {
	t.Run("injoignable", func(t *testing.T) {
		app, mux, cookie := carnetDeTest(t)
		morte := adresseMorte(t)
		avecTelechargement(t, autoriseLesAdresses(morte))

		exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, "http://"+morte+"/photo.png"))
	})

	for nom, code := range map[string]int{"404": http.StatusNotFound, "403": http.StatusForbidden} {
		t.Run("refus HTTP "+nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			serveur := serveurDImages(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			})
			avecTelechargement(t, autoriseLesServeurs(serveur))

			exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, serveur.URL+"/photo.png"))
		})
	}

	t.Run("robots.txt interdit le chemin", func(t *testing.T) {
		app, mux, cookie := carnetDeTest(t)
		serveur := serveurDImagesRobots(t, "User-agent: *\nDisallow: /images/",
			sertLesOctets(pngDeTest(t), "image/png"))
		avecTelechargement(t, autoriseLesServeurs(serveur))

		exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, serveur.URL+"/images/photo.png"))
	})

	t.Run("au-delà du plafond de taille", func(t *testing.T) {
		app, mux, cookie := carnetDeTest(t)
		enorme := append(pngDeTest(t), make([]byte, recuperation.TailleMaxDefaut)...)
		serveur := serveurDImages(t, sertLesOctets(enorme, "image/png"))
		avecTelechargement(t, autoriseLesServeurs(serveur))

		exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, serveur.URL+"/photo.png"))
	})

	t.Run("délai dépassé", func(t *testing.T) {
		app, mux, cookie := carnetDeTest(t)
		serveur := serveurDImages(t, func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
		// Le délai est abaissé : la valeur de production est de dix secondes,
		// et aucun test ne les attend. Ce qui se vérifie ici est que la cause
		// est traitée, pas la valeur du délai — elle est à PATA-8.
		avecTelechargement(t, autoriseLesServeurs(serveur), recuperation.AvecDelaiMax(100*time.Millisecond))

		exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, serveur.URL+"/photo.png"))
	})
}

// --- SSRF, dans le sens du refus (DOD.md §3) --------------------------------

// Critère 10 : le champ caché n'est pas un champ de confiance. Il est posté
// par le client, donc il vise ce que le client veut — la boucle locale, les
// métadonnées de l'hébergeur, le réseau privé de la machine.
func TestUneImageURLVersUneAdresseInterditeNAttacheRien(t *testing.T) {
	cas := map[string]struct {
		adresse string
		table   map[string]string
	}{
		"boucle locale":         {adresse: "http://127.0.0.1:8090/photo.png"},
		"métadonnées de l'hôte": {adresse: "http://169.254.169.254/latest/meta-data"},
		"réseau privé":          {adresse: "http://10.0.0.1/photo.png"},
		"nom public, adresse privée": {
			adresse: "http://images.exemple.fr/photo.png",
			table:   map[string]string{"images.exemple.fr": "10.0.0.1"},
		},
	}

	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			choix := []recuperation.Option{}
			if c.table != nil {
				choix = append(choix, resolutionDeTest(c.table))
			}
			avecTelechargement(t, choix...)

			exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, c.adresse))
			exigeStockageVide(t, app)
		})
	}
}

// Une redirection qui revient vers la boucle locale est refusée comme le
// serait un accès direct : la politique s'applique à chaque saut.
func TestUneRedirectionVersLaBoucleLocaleNAttacheRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	serveur := serveurDImages(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:8090/photo.png", http.StatusFound)
	})
	// Seule l'adresse du serveur est permise : la cible de la redirection, sur
	// la même boucle locale mais un autre port, reste refusée.
	avecTelechargement(t, autoriseLesServeurs(serveur))

	exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, serveur.URL+"/photo.png"))
	exigeStockageVide(t, app)
}

// Critère 10 encore : un schéma que nous ne suivons pas est écarté avant le
// réseau. Le piège le prouve — aucun paquet ne part.
func TestUnSchemaNonSuiviNeDeclencheAucuneRequete(t *testing.T) {
	cas := map[string]string{
		"fichier local": "file:///etc/passwd",
		"données":       "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==",
	}

	for nom, adresse := range cas {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			aucuneRequeteSortante(t)

			exigeRecetteSansImage(t, app, posteAvecImage(t, mux, cookie, adresse))
			exigeStockageVide(t, app)
		})
	}
}

// --- La priorité -----------------------------------------------------------

// Déroulé n° 2 : fichier téléversé > image_url > image déjà stockée. Un
// fichier choisi par l'utilisateur ne déclenche aucune requête sortante — il
// n'y a rien à aller chercher.
func TestUnFichierTeleverseLEmporteSurLImageURL(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	aucuneRequeteSortante(t)

	televerse := pngDeTaille(t, 6, 6)
	champs := champsValides()
	champs.Set("image_url", "https://exemple.fr/autre.png")
	poste(t, mux, "/recettes", cookie, champs, fichierPoste{nom: "tarte.png", contenu: televerse})

	if stocke := fichierStocke(t, app, laRecette(t, app)); !bytes.Equal(stocke, televerse) {
		t.Error("le fichier stocké n'est pas celui que l'utilisateur a téléversé")
	}
}

// --- L'édition -------------------------------------------------------------

// Critère 12 : une édition ordinaire ne touche ni à l'image, ni au réseau.
func TestUneEditionSansImageURLNeTelechargeRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	aucuneRequeteSortante(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	avant := recette.GetString("image")
	if avant == "" {
		t.Fatal("aucune image enregistrée à la création")
	}

	poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides())

	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != avant {
		t.Errorf("image %q après une édition sans image_url, %q attendue", apres, avant)
	}
}

// Le champ caché vaut pour les deux routes d'enregistrement : une règle qui ne
// vaudrait qu'à la création se contournerait sans qu'on s'en aperçoive.
func TestUneEditionAvecUneImageURLRemplaceLImage(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	avant := recette.GetString("image")
	if avant == "" {
		t.Fatal("aucune image enregistrée à la création")
	}

	remplacante := pngDeTaille(t, 7, 7)
	serveur := serveurDImages(t, sertLesOctets(remplacante, "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/remplacante.png")
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	relue := relitLaRecette(t, app, recette.Id)
	if relue.GetString("image") == avant {
		t.Fatal("l'image n'a pas été remplacée par celle qu'image_url désigne")
	}
	if stocke := fichierStocke(t, app, relue); !bytes.Equal(stocke, remplacante) {
		t.Error("le fichier stocké n'est pas celui qu'image_url désignait")
	}
}

// --- L'URL relative --------------------------------------------------------

// Déroulé n° 3 : une image_url relative se résout contre la source_url
// soumise avec le formulaire. Beaucoup de sites n'écrivent que le chemin.
func TestUneImageURLRelativeEstResolueContreLaSource(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	demandes := make(chan string, 4)
	serveur := serveurDImages(t, func(w http.ResponseWriter, r *http.Request) {
		demandes <- r.URL.Path
		sertLesOctets(pngDeTest(t), "image/png")(w, r)
	})
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("source-url", serveur.URL+"/recettes/gratin")
	champs.Set("image_url", "/img/photo.png")
	poste(t, mux, "/recettes", cookie, champs)

	select {
	case chemin := <-demandes:
		if chemin != "/img/photo.png" {
			t.Errorf("chemin demandé %q, /img/photo.png attendu", chemin)
		}
	default:
		t.Fatal("aucune requête n'est partie : l'URL relative n'a pas été résolue")
	}
	if laRecette(t, app).GetString("image") == "" {
		t.Error("aucune image attachée alors que l'URL relative se résout")
	}
}

// L'autre moitié : sans URL absolue à l'arrivée, rien ne part sur le réseau.
func TestUneImageURLNonResolueNeDeclencheAucuneRequete(t *testing.T) {
	cas := map[string]url.Values{
		"champ vide":           {"image_url": {""}},
		"relative sans source": {"image_url": {"/img/photo.png"}},
		// Une adresse sans schéma : le formulaire l'accepte — source_url est
		// un URLField, et « carnet-de-mamie » tout court n'en passerait pas la
		// validation —, mais elle ne résout rien.
		"source non absolue":  {"image_url": {"/img/photo.png"}, "source-url": {"carnet-de-mamie.fr"}},
		"image_url illisible": {"image_url": {"://"}},
	}

	for nom, ajouts := range cas {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			aucuneRequeteSortante(t)

			champs := champsValides()
			for champ, valeurs := range ajouts {
				champs.Set(champ, valeurs[0])
			}
			exigeRecetteSansImage(t, app, poste(t, mux, "/recettes", cookie, champs))
		})
	}
}

// --- La session ------------------------------------------------------------

// Critère 13 : le contrôle de session passe avant tout le reste, donc avant
// toute requête sortante. Une route d'écriture ouverte offrirait ce mandataire
// à qui la trouve.
func TestSansSessionUnPostAvecImageURLNeDeclencheAucuneRequete(t *testing.T) {
	t.Run("création", func(t *testing.T) {
		app, mux, _ := carnetDeTest(t)
		aucuneRequeteSortante(t)

		champs := champsValides()
		champs.Set("image_url", "https://exemple.fr/photo.png")
		rec := poste(t, mux, "/recettes", nil, champs)

		if rec.Code != http.StatusSeeOther {
			t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
		}
		if n := len(recettes(t, app)); n != 0 {
			t.Errorf("%d recettes créées sans session, 0 attendue", n)
		}
	})

	t.Run("édition", func(t *testing.T) {
		app, mux, _ := carnetDeTest(t)
		recette := recetteEnregistree(t, app, nil)
		aucuneRequeteSortante(t)

		champs := champsValides()
		champs.Set("image_url", "https://exemple.fr/photo.png")
		poste(t, mux, "/recettes/"+recette.Id, nil, champs)

		if relitLaRecette(t, app, recette.Id).GetString("image") != "" {
			t.Error("une image a été attachée par un POST sans session")
		}
	})
}

// --- Ce qui ne doit pas fuir -----------------------------------------------

// Critère 14, arbitrage n° 2 : l'échec est silencieux pour l'utilisateur et
// journalisé côté serveur. L'URL sur laquelle il s'est produit est une donnée
// technique — elle n'a rien à faire dans la page rendue.
func TestLErreurTechniqueDuTelechargementNeFuitPasDansLaPage(t *testing.T) {
	const marqueur = "marqueur-tres-improbable-93f2"

	app, mux, cookie := carnetDeTest(t)
	morte := adresseMorte(t)
	avecTelechargement(t, autoriseLesAdresses(morte))

	rec := posteAvecImage(t, mux, cookie, "http://"+morte+"/"+marqueur+".png")

	recette := laRecette(t, app)
	suite := demande(mux, "/recettes/"+recette.Id, cookie, nil)
	exigeSansAucun(t, rec.Body.String(), marqueur)
	exigeSansAucun(t, suite.Body.String(), marqueur)
}

// --- Aides communes --------------------------------------------------------

// posteAvecImage envoie une saisie valide portant cette image_url-là.
func posteAvecImage(t *testing.T, mux http.Handler, cookie *http.Cookie, adresse string) *httptest.ResponseRecorder {
	t.Helper()

	champs := champsValides()
	champs.Set("image_url", adresse)
	return poste(t, mux, "/recettes", cookie, champs)
}

// exigeRecetteSansImage dit ce qu'un échec de téléchargement doit produire :
// la recette est enregistrée, l'utilisateur est renvoyé sur sa fiche, et il
// n'y a pas d'image. Ni 500, ni formulaire re-rendu, ni création empêchée.
func exigeRecetteSansImage(t *testing.T, app core.App, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if nom := laRecette(t, app).GetString("image"); nom != "" {
		t.Errorf("image %q attachée alors que le téléchargement devait échouer", nom)
	}
}
