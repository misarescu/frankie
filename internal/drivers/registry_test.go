package drivers_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/drivers/driverstest"
)

// M7, D112.
func TestRegistry(t *testing.T) {
	r, err := drivers.NewRegistry(&driverstest.Fake{DriverName: "flux"}, &driverstest.Fake{DriverName: "flux-preview"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Names(); !slices.Equal(got, []string{"flux", "flux-preview"}) {
		t.Errorf("Names = %v", got)
	}
	d, err := r.Get("flux")
	if err != nil || d.Name() != "flux" {
		t.Fatalf("Get(flux) = %v, %v", d, err)
	}
	// Drivers come back checked: a release with no commit never reaches the fake.
	bad := releaseRequest()
	bad.Subject.SHA = ""
	if _, err := d.Publish(t.Context(), bad); !errors.Is(err, drivers.ErrInvalidRequest) {
		t.Errorf("Publish through the registry = %v, want ErrInvalidRequest", err)
	}
	_, err = r.Get("argocd")
	if !errors.Is(err, drivers.ErrUnknownDriver) || !strings.Contains(err.Error(), "flux, flux-preview") {
		t.Errorf("Get(argocd) = %v, want ErrUnknownDriver listing the known ones", err)
	}

	if _, err := drivers.NewRegistry(&driverstest.Fake{DriverName: "flux"}, &driverstest.Fake{DriverName: "flux"}); err == nil {
		t.Error("two drivers with one name accepted")
	}
	if _, err := drivers.NewRegistry(&driverstest.Fake{}); err == nil {
		t.Error("a driver without a name accepted")
	}
	empty, err := drivers.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Get("flux"); err == nil || !strings.Contains(err.Error(), "known: none") {
		t.Errorf("Get on an empty registry = %v", err)
	}
}
