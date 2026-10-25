// SPDX-License-Identifier: Apache-2.0

package publicsite_test

import (
	"net/http"
	"testing"
	"time"
)

func TestSiteRefusesPageNumbersThatAreNotPositiveASCIIDigits(t *testing.T) {
	t.Parallel()

	handler, _ := siteWith(publishedPost("A Post", "a-post", blockMarkup, time.Now().UTC()))

	for _, address := range []string{
		"/page/0", "/page/-1", "/page/+2", "/page/２", "/pages/page/0", "/pages/page/+2",
	} {
		recorder := get(t, handler, address)

		if recorder.Code != http.StatusNotFound {
			t.Errorf("status at %q = %d, want %d", address, recorder.Code, http.StatusNotFound)
		}
	}
}

func TestSiteRefusesASignedPageAliasWhenItsDigitEquivalentHoldsContent(t *testing.T) {
	t.Parallel()

	handler, _ := siteWith(storedAt("page", "Two", "pages/page/2"))
	recorder := get(t, handler, "/pages/page/+2")

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d for a signed page alias", recorder.Code, http.StatusNotFound)
	}
}
