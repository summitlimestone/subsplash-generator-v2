// Package appdir locates the folder the app keeps its data in.
package appdir

import (
	"os"
	"path/filepath"
)

// PortableMarker is a file which, placed next to the executable, keeps
// all data in a "data" folder beside it instead of the user profile.
const PortableMarker = "portable"

// Data returns the app's data folder: "data" next to the executable in
// portable mode, else SubsplashGenerator in the user's config folder
// (%APPDATA% on Windows).
func Data() (string, error) {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, PortableMarker)); err == nil {
			return filepath.Join(dir, "data"), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "SubsplashGenerator"), nil
}

// Database returns the path of the app's database in dir.
func Database(dir string) string { return filepath.Join(dir, "subsplash.db") }
