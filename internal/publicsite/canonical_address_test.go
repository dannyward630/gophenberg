// SPDX-License-Identifier: Apache-2.0

package publicsite_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/gopherium/gophenberg/internal/content"
)

func TestSiteSendsAListingPageToTheOneAddressItAnswersAt(t *testing.T) {
	t.Parallel()

	news := content.Content{
		ID: uuid.Must(uuid.NewV7()), Type: "category", Status: content.StatusPublished,
		Path: "categories/news", Slug: "news", Title: "News", Content: blockMarkup,
	}
	handler, _ := siteWith(append(manyPosts(25), news)...)

	for _, tc := range []struct {
		requested string
		want      string
	}{
		{"/page/1", "/"},
		{"/page/01", "/"},
		{"/page/02", "/page/2"},
		{"/page/+2", "/page/2"},
		{"/page/1?utm_source=feed", "/?utm_source=feed"},
		{"/page/02?utm_source=feed", "/page/2?utm_source=feed"},
		{"/pages/page/1", "/pages"},
		{"/pages/", "/pages"},
		{"/pages/page/2/", "/pages/page/2"},
		{"/page/2/", "/page/2"},
		{"/categories/news/", "/categories/news"},
		{"/pages/page/007", "/pages/page/7"},
		{"/categories/news/page/1", "/categories/news"},
		{"/categories/news/page/03", "/categories/news/page/3"},
	} {
		recorder := get(t, handler, tc.requested)

		if recorder.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s status = %d, want %d", tc.requested, recorder.Code, http.StatusMovedPermanently)
		}
		if got := recorder.Header().Get("Location"); got != tc.want {
			t.Errorf("GET %s Location = %q, want %q", tc.requested, got, tc.want)
		}
	}
}

func TestSiteKeepsTheAddressesAListingAlreadyAnswersAt(t *testing.T) {
	t.Parallel()

	news := content.Content{
		ID: uuid.Must(uuid.NewV7()), Type: "category", Status: content.StatusPublished,
		Path: "categories/news", Slug: "news", Title: "News", Content: blockMarkup,
	}
	nested := publishedPost("One", "1", blockMarkup, time.Now().UTC())
	nested.Type, nested.Path = "page", "pages/page/1"
	handler, _ := siteWith(append(manyPosts(25), news, nested)...)

	for _, address := range []string{
		"/", "/page/2", "/pages", "/pages/page/2", "/pages/page/1",
		"/categories/news", "/categories/news/page/2",
	} {
		recorder := get(t, handler, address)

		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", address, recorder.Code, http.StatusOK)
		}
	}
}
