package migrations

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// Une installation fraîche de PocketBase a `Backups.Cron` vide, donc aucune
// sauvegarde automatique : l'auto-hébergeur qui ne va jamais dans /_/ n'a rien.
//
// Les valeurs sont écrites en clair ici, pas relues depuis la migration : un
// test qui compare une constante à elle-même ne vérifie que lui-même.
func TestUneBaseNeuveSauvegardeChaqueNuit(t *testing.T) {
	app := baseNeuve(t)

	sauvegardes := app.Settings().Backups

	if sauvegardes.Cron != "0 3 * * *" {
		t.Errorf("Backups.Cron = %q, attendu %q : sans expression cron, PocketBase ne sauvegarde jamais de lui-même",
			sauvegardes.Cron, "0 3 * * *")
	}
	if sauvegardes.CronMaxKeep != 3 {
		t.Errorf("Backups.CronMaxKeep = %d, attendu 3", sauvegardes.CronMaxKeep)
	}
}

// La preuve que « PocketBase sait le faire » se traduit par « Patachoo le
// fait » : une archive prise sur une base qui porte une recette et son image
// doit contenir la base *et* le fichier. Une sauvegarde qui n'emporterait que
// data.db rendrait des recettes sans photo — et ça ne se voit qu'à la
// restauration, c'est-à-dire trop tard.
func TestLArchiveEmporteLaBaseEtLesImages(t *testing.T) {
	app := baseNeuve(t)

	recettes, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatal(err)
	}

	recette := core.NewRecord(recettes)
	recette.Set("title", "Tarte aux pommes")
	photo, err := filesystem.NewFileFromBytes(pngMinimal(t), "tarte.png")
	if err != nil {
		t.Fatal(err)
	}
	recette.Set("image", photo)
	if err := app.Save(recette); err != nil {
		t.Fatal(err)
	}

	nomDeLImage := recette.GetString("image")
	if nomDeLImage == "" {
		t.Fatal("la recette enregistrée ne porte aucun fichier : la suite du test ne prouverait rien")
	}

	if err := app.CreateBackup(context.Background(), "essai.zip"); err != nil {
		t.Fatalf("CreateBackup : %v", err)
	}

	archive := filepath.Join(app.DataDir(), core.LocalBackupsDirName, "essai.zip")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("archive absente : %v", err)
	}

	entrees := contenuDeLArchive(t, archive)

	if !entrees[nomDuFichierDeBase] {
		t.Errorf("%s absent de l'archive : elle ne contient pas la base", nomDuFichierDeBase)
	}

	cheminAttendu := core.LocalStorageDirName + "/" + recettes.Id + "/" + recette.Id + "/" + nomDeLImage
	if !entrees[cheminAttendu] {
		t.Errorf("%s absent de l'archive : les images ne seraient pas restaurées", cheminAttendu)
	}
}

// nomDuFichierDeBase : RestoreBackup refuse une archive qui ne le porte pas.
const nomDuFichierDeBase = "data.db"

// contenuDeLArchive rend l'ensemble des chemins portés par l'archive zip.
func contenuDeLArchive(t *testing.T, chemin string) map[string]bool {
	t.Helper()

	lecteur, err := zip.OpenReader(chemin)
	if err != nil {
		t.Fatalf("ouverture de l'archive : %v", err)
	}
	t.Cleanup(func() { _ = lecteur.Close() })

	entrees := map[string]bool{}
	for _, fichier := range lecteur.File {
		entrees[fichier.Name] = true
	}
	return entrees
}

// pngMinimal rend une image PNG valide : le champ `image` n'accepte que des
// types réels, un fichier texte renommé serait refusé à l'enregistrement.
func pngMinimal(t *testing.T) []byte {
	t.Helper()

	var tampon bytes.Buffer
	if err := png.Encode(&tampon, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return tampon.Bytes()
}
