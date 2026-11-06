package telemetryingest

import (
	"sort"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// platformNamespace prefixes every resource attribute the platform
// attributes telemetry by: the Work, its Agent, Project, person, owner and
// Computer. The machine's own attributes there are dropped whatever they
// say; the run's attribution alone takes their place. Attributes outside it,
// such as service.name and the host's, describe the process and stay.
const platformNamespace = "talgent."

// attributed is resource with the run's attribution in place of whatever
// it carried in the platform's namespace.
func attributed(resource *resourcepb.Resource, attributes map[string]string) *resourcepb.Resource {
	if resource == nil {
		resource = &resourcepb.Resource{}
	}
	kept := resource.GetAttributes()[:0]
	for _, attribute := range resource.GetAttributes() {
		if attribute == nil || strings.HasPrefix(attribute.GetKey(), platformNamespace) {
			continue
		}
		kept = append(kept, attribute)
	}
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		kept = append(kept, &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: attributes[key]}}})
	}
	resource.Attributes = kept
	return resource
}
