//go:build !unix

package main

// resserreLesDroits ne fait rien là où syscall.Umask n'existe pas — Windows et
// Plan 9, pour lesquels le dépôt compile aujourd'hui.
//
// Le jumeau plutôt qu'un appel gardé par une condition : main appelle toujours
// la même fonction, et rien dans le chemin principal n'a à savoir sur quelle
// plateforme il tourne.
func resserreLesDroits() {}
