package tray

// Autostart starts the icon when the user logs in. It is the only thing that
// makes the icon permanent, and it is off until the person switches it on.
type Autostart interface {
	// Enable registers the command that starts the icon.
	Enable(executable string) error
	// Disable removes the registration. Nothing registered is not an error.
	Disable() error
	// Enabled says whether the registration exists.
	Enabled() (bool, error)
	// Supported says whether this OS can do it.
	Supported() bool
}
