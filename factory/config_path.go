package factory

type Path struct {
	DestinationIP string `yaml:"DestinationIP,omitempty"`

	DestinationPort uint16 `yaml:"DestinationPort,omitempty"`

	UPF []string `yaml:"UPF,omitempty"`
}
