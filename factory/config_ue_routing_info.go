/*
 * SMF Routing Configuration Factory
 */

package factory

type UERoutingInfo struct {
	SUPI string `yaml:"SUPI,omitempty"`

	PathList []Path `yaml:"PathList,omitempty"`
}
