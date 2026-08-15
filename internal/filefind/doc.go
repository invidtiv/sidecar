// Package filefind provides the file-finding primitives shared by any surface
// that offers "open a file by typing part of its name": fuzzy matching and
// scoring, the gitignore rules the scan honours, and a background project file
// cache that keeps its previous answer visible while it rescans.
package filefind
