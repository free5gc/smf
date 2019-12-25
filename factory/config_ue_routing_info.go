/*
 * SMF Routing Configuration Factory
 */

package factory

type UERoutingInfo struct {
	IMSI string `yaml:"IMSI,omitempty"`

	PathList []Path `yaml:"PathList,omitempty"`
}
