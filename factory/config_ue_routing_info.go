/*
 * SMF Routing Configuration Factory
 */

package factory

type UERoutingInfo struct {
	IMSI string `yaml:"IMSI,omitempty"`

	pathList []Path `yaml:"pathList,omitempty"`
}
