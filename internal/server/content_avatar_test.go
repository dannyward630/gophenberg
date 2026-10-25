// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/gopherium/gouncer"
	"github.com/gopherium/gouncer/authkit/testkit"
	"github.com/gopherium/gouncer/avatar"

	"github.com/gopherium/gophenberg/internal/content"
	"github.com/gopherium/gophenberg/internal/role"
	"github.com/gopherium/gophenberg/internal/server"
)

// authorCell is the avatar size the admin's author cell asks for, twice the 16 pixels it draws.
const authorCell = 32

// errAccountsDown stands for an account store that cannot read many accounts by id.
var errAccountsDown = errors.New("the accounts are unreachable")

// avatarBody is the title and the author part of one content item the admin reads.
type avatarBody struct {
	Title        string  `json:"title"`
	AuthorName   string  `json:"author_name"`
	AuthorAvatar *string `json:"author_avatar"`
}

// itemAuthor is who wrote one listed item, by name and email.
type itemAuthor struct {
	name  string
	email string
}

// secondAuthor is the writer of the avatar tests beside Maria Perez.
var secondAuthor = itemAuthor{name: "Second Author", email: "second@example.com"}

// authoredPosts are the posts authoredServer stores, each title with its author.
var authoredPosts = map[string]itemAuthor{
	"Hello World": {name: "Maria Perez", email: "maria@example.com"},
	"Notes":       secondAuthor,
}

// addAuthors stores the signed-in administrator, then Maria Perez and secondAuthor as authors, and returns both.
func addAuthors(t *testing.T, users *testkit.Store) (gouncer.User, gouncer.User) {
	t.Helper()
	addAda(t, users)
	maria := addWithRole(t, users, "maria@example.com", "Maria Perez", role.Author)
	return maria, addWithRole(t, users, secondAuthor.email, secondAuthor.name, role.Author)
}

// avatarListBody is the admin listing as the author cell reads it.
type avatarListBody struct {
	Items []avatarBody `json:"items"`
}

// unreadableByID is an account store whose read of many accounts by id always fails.
type unreadableByID struct {
	*testkit.Store
}

// UsersByIDs reports a failure.
func (unreadableByID) UsersByIDs(context.Context, []uuid.UUID) ([]gouncer.User, error) {
	return nil, errAccountsDown
}

// gravatarOptions draws avatars from Gravatar with choices away from every default.
var gravatarOptions = avatar.Options{
	Mode: avatar.ModeGravatar, Default: avatar.DefaultIdenticon, Rating: avatar.RatingPG,
}

// authoredServer returns a signed-in server drawing avatars under options over wrapped users, and Maria Perez's post.
func authoredServer(
	t *testing.T, options avatar.Options, wrap func(*testkit.Store) server.UserStore,
) (http.Handler, content.Content) {
	t.Helper()
	users := newFakeUserStore()
	maria, second := addAuthors(t, users)
	posts := newFakePostStore()
	posts.authors = map[uuid.UUID]string{maria.ID: maria.Name, second.ID: second.Name}
	post := posts.add(newPost(t, "Hello World", maria.ID))
	posts.add(newPost(t, "Notes", second.ID))
	handler := authedServerWithStores(t, server.Config{
		Users: wrap(users), Content: posts, Types: newFakeTypeStore(), Avatars: options,
	})
	return handler, post
}

// readable reads the users as they are stored.
func readable(users *testkit.Store) server.UserStore {
	return users
}

// unreadable reads the users through a store whose read by id fails.
func unreadable(users *testkit.Store) server.UserStore {
	return unreadableByID{Store: users}
}

// avatarCases are the site's avatar choices the author cell follows.
var avatarCases = map[string]avatar.Options{
	"initials by default": {},
	"gravatar":            gravatarOptions,
	"off":                 {Mode: avatar.ModeOff},
}

// wantedAvatar returns the address the author cell must receive for the writer under options, empty with avatars off.
func wantedAvatar(writer itemAuthor, options avatar.Options) string {
	if options.Mode == avatar.ModeOff {
		return ""
	}
	return avatar.Address(writer.name, writer.email, authorCell, options)
}

// checkListedAuthors fails for each listed item lacking its author in authored or that author's avatar under options.
func checkListedAuthors(t *testing.T, body avatarListBody, options avatar.Options, authored map[string]itemAuthor) {
	t.Helper()
	if len(body.Items) != len(authored) {
		t.Fatalf("body = %+v, want the %d items", body, len(authored))
	}
	for _, item := range body.Items {
		writer := authored[item.Title]
		got, want := avatarOf(t, item), wantedAvatar(writer, options)
		if item.AuthorName != writer.name || got != want {
			t.Errorf("%q is by %q with author_avatar %q, want by %q with %q",
				item.Title, item.AuthorName, got, writer.name, want)
		}
	}
}

// avatarOf returns the avatar address the author part carries, failing when the key is missing.
func avatarOf(t *testing.T, body avatarBody) string {
	t.Helper()
	if body.AuthorAvatar == nil {
		t.Fatalf("item %+v carries no author_avatar, want the key on every item", body)
	}
	return *body.AuthorAvatar
}

func TestContentListSendsEachAuthorsAvatarAtTwiceTheAuthorCell(t *testing.T) {
	t.Parallel()

	for name, options := range avatarCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, _ := authoredServer(t, options, readable)

			recorder := doRequest(t, handler, http.MethodGet, "/api/content", "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}
			checkListedAuthors(t, decodeBody[avatarListBody](t, recorder), options, authoredPosts)
		})
	}
}

func TestContentGetSendsTheAuthorsAvatarAtTwiceTheAuthorCell(t *testing.T) {
	t.Parallel()

	for name, options := range avatarCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, post := authoredServer(t, options, readable)

			recorder := doRequest(t, handler, http.MethodGet, "/api/content/"+post.ID.String(), "")

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
			}
			want := wantedAvatar(authoredPosts[post.Title], options)
			if got := avatarOf(t, decodeBody[avatarBody](t, recorder)); got != want {
				t.Errorf("author_avatar = %q, want %q", got, want)
			}
		})
	}
}

func TestContentAvatarsReadNoAccountUnlessGravatarDrawsThem(t *testing.T) {
	t.Parallel()

	for name, options := range map[string]avatar.Options{
		"initials": {Mode: avatar.ModeInitials},
		"off":      {Mode: avatar.ModeOff},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, post := authoredServer(t, options, unreadable)

			for _, path := range []string{"/api/content", "/api/content/" + post.ID.String()} {
				if recorder := doRequest(t, handler, http.MethodGet, path, ""); recorder.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want %d without reading accounts: %s",
						path, recorder.Code, http.StatusOK, recorder.Body)
				}
			}
		})
	}
}

func TestContentGravatarListReportsAFailedAccountRead(t *testing.T) {
	t.Parallel()

	handler, _ := authoredServer(t, gravatarOptions, unreadable)

	recorder := doRequest(t, handler, http.MethodGet, "/api/content", "")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("GET /api/content = %d, want %d when the author emails cannot be read",
			recorder.Code, http.StatusInternalServerError)
	}
}

func TestContentGravatarItemReadsItsAuthorFromTheAccountList(t *testing.T) {
	t.Parallel()

	handler, post := authoredServer(t, gravatarOptions, unreadable)

	read := doRequest(t, handler, http.MethodGet, "/api/content/"+post.ID.String(), "")
	created := doRequest(t, handler, http.MethodPost, "/api/content", `{"title":"Hello Again"}`)

	if read.Code != http.StatusOK || created.Code != http.StatusCreated {
		t.Fatalf("GET = %d and POST = %d, want %d and %d without a read by id: %s %s",
			read.Code, created.Code, http.StatusOK, http.StatusCreated, read.Body, created.Body)
	}
	want := avatar.Address("Ada Lovelace", "ada@example.com", authorCell, gravatarOptions)
	if got := avatarOf(t, decodeBody[avatarBody](t, created)); got != want {
		t.Errorf("author_avatar of the stored post = %q, want %q", got, want)
	}
}
