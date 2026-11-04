package release

// #332: Orbit v0.3.6's signed manifest gains the arrays updateFrom and
// releases (schema stays v1). The launcher ignores them and reads its
// existing fields unchanged.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckForUpdate_ManifestWithUpdateFromAndReleasesReadsExistingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"schema":"https://tomlawson.io/schemas/orbit-release-manifest/v1","version":"0.3.6",`+
			`"updateFrom":["v0.3.4","v0.3.5"],"releases":["v0.3.4","v0.3.5","v0.3.6"],`+
			`"launcher":{"tag":"v0.2.0","commit":"0123456789012345678901234567890123456789"}}`)
	}))
	t.Cleanup(srv.Close)

	latest, hasUpdate, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if latest != "v0.2.0" || !hasUpdate {
		t.Errorf("latest, hasUpdate = %q, %v; want v0.2.0, true", latest, hasUpdate)
	}
}
