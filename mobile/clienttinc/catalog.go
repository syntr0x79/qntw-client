package clienttinc

import (
	"context"
	"errors"

	"github.com/google/uuid"
	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

// Catalog is read-only and may be requested before enrolling the local key.
func (a *API) Catalog(ctx context.Context) (wire.ClientCatalog, error) {
	var catalog wire.ClientCatalog
	if err := a.call(ctx, "GET", "catalog", nil, &catalog); err != nil {
		return catalog, err
	}
	validState := func(s string) bool { return s == "available" || s == "preparing" || s == "unavailable" }
	if catalog.SchemaVersion != wire.SchemaVersion || catalog.DeviceID == uuid.Nil || !validState(catalog.State) || len(catalog.Countries) > 250 {
		return wire.ClientCatalog{}, errors.New("invalid connection catalog")
	}
	seen := make(map[string]bool)
	for _, country := range catalog.Countries {
		if !safeCountry.MatchString(country.Code) || !validState(country.State) || seen[country.Code] {
			return wire.ClientCatalog{}, errors.New("invalid connection catalog")
		}
		seen[country.Code] = true
	}
	return catalog, nil
}
