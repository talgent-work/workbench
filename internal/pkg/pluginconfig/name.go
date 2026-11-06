package pluginconfig

import "regexp"

// Native Plugin identifiers preserve package spelling, including case. They must
// remain safe namespace and directory segments; they are not display labels.
var nativePluginName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ValidPluginName(name string) bool { return nativePluginName.MatchString(name) }
