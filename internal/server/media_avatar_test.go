// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/gopherium/gouncer/authkit/testkit"
	"github.com/gopherium/gouncer/avatar"

	"github.com/gopherium/gophenberg/internal/media"
	"github.com/gopherium/gophenberg/internal/mediahost"
	"github.com/gopherium/gophenberg/internal/server"
)

// uploadedMedia are the files authoredMediaServer stores, each title with who uploaded it.
var uploadedMedia = map[string]itemAuthor{
	"harbor":  {name: "Maria Perez", email: "maria@example.com"},
	"sunrise": secondAuthor,
}

// authoredMediaServer returns a signed-in server holding uploadedMedia, drawing avatars under options over users.
func authoredMediaServer(
	t *testing.T, options avatar.Options, wrap func(*testkit.Store) server.UserStore,
) http.Handler {
	t.Helper()
	users := newFakeUserStore()
	maria, second := addAuthors(t, users)
	store := newFakeMediaStore()
	store.authors = map[uuid.UUID]string{maria.ID: maria.Name, second.ID: second.Name}
	accounts := map[string]uuid.UUID{maria.Email: maria.ID, second.Email: second.ID}
	for title, uploader := range uploadedMedia {
		if _, err := store.Create(t.Context(), media.Media{
			Type: media.TypeImage, Title: title, MimeType: "image/jpeg", AuthorID: accounts[uploader.email],
		}); err != nil {
			t.Fatalf("storing %q: %v", title, err)
		}
	}
	return authedServerWithStores(t, server.Config{
		Users: wrap(users), Content: newFakePostStore(), Types: newFakeTypeStore(),
		Media: mediahost.New(mediahost.Config{Dir: t.TempDir()}), MediaStore: store, Avatars: options,
	})
}

func TestMediaListSendsEachUploadersAvatarAtTwiceTheAuthorCell(t *testing.T) {
	t.Parallel()

	for name, options := range avatarCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := authoredMediaServer(t, options, readable)

			recorder := doRequest(t, handler, http.MethodGet, "/api/media", "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}
			checkListedAuthors(t, decodeBody[avatarListBody](t, recorder), options, uploadedMedia)
		})
	}
}

func TestMediaAvatarsReadNoAccountUnlessGravatarDrawsThem(t *testing.T) {
	t.Parallel()

	for name, options := range map[string]avatar.Options{
		"initials": {Mode: avatar.ModeInitials},
		"off":      {Mode: avatar.ModeOff},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := authoredMediaServer(t, options, unreadable)

			recorder := doRequest(t, handler, http.MethodGet, "/api/media", "")

			if recorder.Code != http.StatusOK {
				t.Errorf("GET /api/media = %d, want %d without reading accounts: %s",
					recorder.Code, http.StatusOK, recorder.Body)
			}
		})
	}
}

func TestMediaGravatarListReportsAFailedAccountRead(t *testing.T) {
	t.Parallel()

	handler := authoredMediaServer(t, gravatarOptions, unreadable)

	recorder := doRequest(t, handler, http.MethodGet, "/api/media", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("GET /api/media = %d, want %d when the uploader emails cannot be read",
			recorder.Code, http.StatusInternalServerError)
	}
}
