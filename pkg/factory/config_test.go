package factory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/free5gc/openapi/models"
	"github.com/free5gc/smf/pkg/factory"
)

func TestSnssaiValidator(t *testing.T) {
	testcase := []struct {
		Name   string
		Snssai *models.Snssai
		Valid  bool
	}{
		{
			Name: "Valid SNssai with SST and SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "010203",
			},
			Valid: true,
		},
		{
			Name: "Valid SNssai without SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
			},
			Valid: true,
		},
		{
			Name: "Invalid SNssai: invalid SST",
			Snssai: &models.Snssai{
				Sst: int32(256),
			},
			Valid: false,
		},
		{
			Name: "Invalid SNssai: invalid SD",
			Snssai: &models.Snssai{
				Sst: int32(1),
				Sd:  "32",
			},
			Valid: false,
		},
	}

	for _, tc := range testcase {
		t.Run(tc.Name, func(t *testing.T) {
			ok, err := factory.ValidateSNssai(tc.Snssai)
			require.Equal(t, tc.Valid, ok)
			if !ok {
				require.Error(t, err)
			} else {
				require.Nil(t, err)
			}
		})
	}
}
