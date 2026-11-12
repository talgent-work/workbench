package credential

import "time"

// ServiceMaterial remains inside trusted transport and outbound injection.
type ServiceMaterial struct {
	Token     string
	ExpiresAt time.Time
}
