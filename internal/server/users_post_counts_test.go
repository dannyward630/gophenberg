// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/gopherium/gophenberg/internal/content"
	"github.com/gopherium/gophenberg/internal/role"
	"github.com/gopherium/gophenberg/internal/server"
)

// postCountsPath is the route answering how many posts each account wrote.
const postCountsPath = "/api/users/post-counts"

// postCountsAnswer is the post counts answer, by account id.
type postCountsAnswer struct {
	Counts map[uuid.UUID]int `json:"counts"`
}

// postHolding returns a post by the author holding the status.
func postHolding(t *testing.T, title string, author uuid.UUID, status content.Status) content.Content {
	t.Helper()
	post := newPost(t, title, author)
	post.Status = status
	return post
}

func TestUserPostCountsHoldEachAccountsPublishedAndPrivatePosts(t *testing.T) {
	t.Parallel()

	users, posts := newFakeUserStore(), newFakePostStore()
	ada := addAda(t, users)
	maria := addWithRole(t, users, "maria@example.com", "Maria Perez", role.Author)
	posts.add(postHolding(t, "Morning Notes", ada.ID, content.StatusPublished))
	posts.add(postHolding(t, "Private Notes", ada.ID, content.StatusPrivate))
	posts.add(postHolding(t, "Evening Draft", ada.ID, content.StatusDraft))
	posts.add(postHolding(t, "Written by Maria", maria.ID, content.StatusPublished))
	page := postHolding(t, "About", maria.ID, content.StatusPublished)
	page.Type = "page"
	posts.add(page)
	handler := authedServerWithStores(t, serverConfig(users, posts))

	recorder := doRequest(t, handler, http.MethodGet, postCountsPath, "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	held := decodeBody[postCountsAnswer](t, recorder)
	if want := map[uuid.UUID]int{ada.ID: 2, maria.ID: 1}; !maps.Equal(held.Counts, want) {
		t.Errorf("counts = %v, want two posts for the administrator and one for Maria Perez", held.Counts)
	}
}

func TestUserPostCountsAnswerNoCountsWithoutAnActivePostType(t *testing.T) {
	t.Parallel()

	off := postType()
	off.Active, off.Default = false, false
	cases := []struct {
		name  string
		types []content.Type
	}{
		{"an inactive post type", []content.Type{off}},
		{"no post type", []content.Type{spareType()}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			users, posts := newFakeUserStore(), newFakePostStore()
			ada := addAda(t, users)
			posts.add(postHolding(t, "Morning Notes", ada.ID, content.StatusPublished))
			handler := authedServerWithStores(t, server.Config{
				Users: users, Content: posts, Types: &fakeTypeStore{types: c.types},
			})

			recorder := doRequest(t, handler, http.MethodGet, postCountsPath, "")

			body := strings.TrimSpace(recorder.Body.String())
			if recorder.Code != http.StatusOK || body != `{"counts":{}}` {
				t.Errorf("GET %s = %d %s, want 200 and no counts", postCountsPath, recorder.Code, body)
			}
		})
	}
}

func TestUserPostCountsReportWhatTheyCannotRead(t *testing.T) {
	t.Parallel()

	for name, fail := range map[string]func(*fakePostStore, *fakeTypeStore){
		"the store":    func(posts *fakePostStore, _ *fakeTypeStore) { posts.authoredErr = context.DeadlineExceeded },
		"the registry": func(_ *fakePostStore, types *fakeTypeStore) { types.listErr = errRegistryDown },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			users, posts, types := newFakeUserStore(), newFakePostStore(), newFakeTypeStore()
			addAda(t, users)
			fail(posts, types)
			handler := authedServerWithStores(t, server.Config{Users: users, Content: posts, Types: types})

			recorder := doRequest(t, handler, http.MethodGet, postCountsPath, "")

			if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal" {
				t.Errorf("GET %s with %s failing = %d %s, want 500 internal",
					postCountsPath, name, recorder.Code, recorder.Body)
			}
		})
	}
}
