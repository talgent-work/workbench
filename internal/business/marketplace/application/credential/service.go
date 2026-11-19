package credential

import "time"

// ServiceMaterial remains inside trusted transport and outbound injection.
type ServiceMaterial struct {
	Token     string
	ExpiresAt time.Time
	// Hosts are the target's declared host patterns, the only hosts the
	// credential may be added to.
	Hosts []string
}
