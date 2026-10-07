// Command notices writes THIRD_PARTY_NOTICES.txt for a release: ffmpeg's
// license and where its source is, then the license of every Go module
// and web UI package built into the app.
//
//	go run ./packaging/notices -ffmpeg-version n8.1.3 -ffmpeg-license LICENSE.txt \
//		-ffmpeg-source https://... -ui frontend/dist/.vite/license.md -o THIRD_PARTY_NOTICES.txt
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const header = `Subsplash Generator is MIT licensed (see LICENSE.txt). It includes the
third-party software below, under the licenses that follow.

================================================================================
ffmpeg %s
================================================================================

The "ffmpeg" folder holds ffmpeg and ffprobe, built by BtbN
(https://github.com/BtbN/FFmpeg-Builds). They are separate programs from
Subsplash Generator, which runs them but doesn't link to them. They are
licensed under the GNU General Public License, version 3 or later; the full
text is in ffmpeg\LICENSE.txt.

You may get the complete source code of this build of ffmpeg, and the build
scripts that name the exact version of every library it includes, from:

  %s

`

func main() {
	ffVersion := flag.String("ffmpeg-version", "", "the bundled ffmpeg's version")
	ffLicense := flag.String("ffmpeg-license", "", "the bundled ffmpeg's LICENSE.txt")
	ffSource := flag.String("ffmpeg-source", "", "where its source is published")
	ui := flag.String("ui", "frontend/dist/.vite/license.md", "Vite's license file for the web UI")
	out := flag.String("o", "THIRD_PARTY_NOTICES.txt", "output file")
	goos := flag.String("goos", "windows", "the platform the app is built for")
	flag.Parse()
	log.SetFlags(0)
	if *ffVersion == "" || *ffLicense == "" || *ffSource == "" {
		log.Fatal("-ffmpeg-version, -ffmpeg-license and -ffmpeg-source are required")
	}
	if _, err := os.Stat(*ffLicense); err != nil {
		log.Fatal(err)
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, header, *ffVersion, *ffSource)

	mods, err := goModules("./cmd/app", *goos)
	if err != nil {
		log.Fatal(err)
	}
	goroot, goversion, err := goEnv()
	if err != nil {
		log.Fatal(err)
	}
	section(&b, "Go "+strings.TrimPrefix(goversion, "go"), []string{filepath.Join(goroot, "LICENSE")})
	for _, m := range mods {
		section(&b, m.name, m.licenses)
	}

	uiText, err := os.ReadFile(*ui)
	if err != nil {
		log.Fatal("the web UI's licenses (build it first): ", err)
	}
	fmt.Fprintf(&b, "%s\nWeb UI packages\n%s\n\n%s\n", rule, rule, uiText)

	if err := os.WriteFile(*out, crlf(b.Bytes()), 0o644); err != nil {
		log.Fatal(err)
	}
}

var rule = strings.Repeat("=", 80)

func section(b *bytes.Buffer, name string, files []string) {
	fmt.Fprintf(b, "%s\n%s\n%s\n", rule, name, rule)
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(b, "\n%s\n", bytes.TrimSpace(text))
	}
	b.WriteString("\n")
}

// goEnv returns the root and version of the Go that builds the app, whose
// standard library is linked into it.
func goEnv() (root, version string, err error) {
	out, err := exec.Command("go", "env", "GOROOT", "GOVERSION").Output()
	if err != nil {
		return "", "", err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return "", "", fmt.Errorf("go env: %q", out)
	}
	return f[0], f[1], nil
}

type module struct {
	name     string
	licenses []string
}

// goModules returns every module that pkg links, but not pkg's own,
// with the license and notice files found in its root and in the
// directories of the packages used from it.
func goModules(pkg, goos string) ([]module, error) {
	cmd := exec.Command("go", "list", "-deps", "-json=Dir,Module", pkg)
	cmd.Env = append(os.Environ(), "GOOS="+goos)
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	type listed struct {
		Dir    string
		Module *struct {
			Path, Version, Dir string
			Main               bool
			Replace            *struct{ Path, Version, Dir string }
		}
	}
	byPath := map[string]*module{}
	seen := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var p listed
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		m := p.Module
		if m == nil || m.Main {
			continue
		}
		root, version := m.Dir, m.Version
		if m.Replace != nil {
			root, version = m.Replace.Dir, m.Replace.Version
		}
		mod := byPath[m.Path]
		if mod == nil {
			mod = &module{name: m.Path + " " + version}
			byPath[m.Path] = mod
		}
		// From the package's folder up to the module root.
		for dir := p.Dir; ; dir = filepath.Dir(dir) {
			for _, f := range licenseFiles(dir) {
				if !seen[f] {
					seen[f] = true
					mod.licenses = append(mod.licenses, f)
				}
			}
			if dir == root || len(dir) <= len(root) {
				break
			}
		}
	}
	var mods []module
	for _, m := range byPath {
		if len(m.licenses) == 0 {
			return nil, fmt.Errorf("no license file found for %s", m.name)
		}
		slices.Sort(m.licenses)
		mods = append(mods, *m)
	}
	slices.SortFunc(mods, func(a, b module) int { return strings.Compare(a.name, b.name) })
	return mods, nil
}

func licenseFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := strings.ToUpper(e.Name())
		if e.IsDir() {
			continue
		}
		for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS"} {
			if strings.HasPrefix(n, prefix) {
				out = append(out, filepath.Join(dir, e.Name()))
				break
			}
		}
	}
	return out
}

// crlf gives Windows line endings, for Notepad.
func crlf(b []byte) []byte {
	return bytes.ReplaceAll(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
}
