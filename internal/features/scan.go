package features

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// A repo can hold several projects of one kind: a monorepo of Maven
// projects, of npm packages, of Python projects, of compose stacks. Every
// feature finds them the same way, by searching below the repo root for
// the files that mark one, and the commands then ask which project to
// use (see projects.go).

const (
	maxProjectDepth = 4   // folders below the repo root that are searched
	maxScanned      = 500 // projects read per feature
	maxScanDirs     = 20000
)

// skipDirs are never searched: build output, dependencies, environments.
var skipDirs = map[string]bool{
	"node_modules": true, "target": true, "src": true, "build": true, "dist": true, "out": true,
	"vendor": true, "venv": true, "env": true, "__pycache__": true,
}

// walkFor calls visit with the folder (relative to repoRoot, slash
// separated, "." for the root) and the name of each file whose name match
// accepts, up to maxProjectDepth folders down, until visit returns false.
// Hidden folders and skipDirs are skipped, and symlinked folders are not
// followed, so nothing outside the repo is read.
func walkFor(repoRoot string, match func(name string) bool, visit func(rel, name string) bool) {
	visited := 0
	stop := false
	_ = filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || stop {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if visited++; visited > maxScanDirs {
				stop = true
				return fs.SkipAll
			}
			if rel != "." {
				if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || strings.Count(rel, "/") >= maxProjectDepth {
					return fs.SkipDir
				}
			}
			return nil
		}
		if match(d.Name()) {
			dir := filepath.ToSlash(filepath.Dir(rel))
			if !visit(dir, d.Name()) {
				stop = true
				return fs.SkipAll
			}
		}
		return nil
	})
}

// projectDirs lists the folders (relative, "." for the root) holding any
// file named in names, sorted, without those whose name can't be a
// project target (see projectDirOK).
func projectDirs(repoRoot string, names ...string) []string {
	set := map[string]bool{}
	walkFor(repoRoot, func(n string) bool { return contains(names, n) }, func(rel, _ string) bool {
		if projectDirOK(rel) {
			set[rel] = true
		}
		return len(set) < maxScanned
	})
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
