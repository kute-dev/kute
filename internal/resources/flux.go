// Flux CD kinds (docs/design README.md §30a). Flux's five API groups are a
// fixed, versioned, documented status vocabulary rather than the unknown
// long tail CustomDescriptor serves, so they get a curated descriptor —
// the same narrow exception §23b already makes for HTTPRoute, and for the
// same reason: their health question is not "does Ready say True".
//
// Recognition happens in BuildDiscoveredRegistry by API group, never by
// bare Kind name (see kube.IsFluxGroup).
package resources

import (
	"github.com/kute-dev/kute/internal/kube"
)

// fluxDescriptor builds the §30a list Descriptor for one discovered Flux
// kind.
func fluxDescriptor(dk kube.DiscoveredKind) Descriptor {
	d := CustomDescriptor(dk)
	d.Group = GroupFlux
	d.Icon = "⇅"
	d.Describe = "flux resource · " + dk.Group
	d.Flux = true
	return d
}
