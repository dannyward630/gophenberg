// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/gopherium/gouncer/avatar"

	"github.com/gopherium/gophenberg/internal/role"
)

// usersListCell is the avatar size the users list asks for, twice the 48 pixels its phone cell draws.
const usersListCell = 96

// listedAccount is one account of the users list as its avatar column reads it.
type listedAccount struct {
	Name   string  `json:"name"`
	Email  string  `json:"email"`
	Avatar *string `json:"avatar"`
}

// avatarUsersServer returns a signed-in server drawing avatars under options over the administrator and Maria Perez.
func avatarUsersServer(t *testing.T, options avatar.Options) http.Handler {
	t.Helper()
	users := newFakeUserStore()
	addAda(t, users)
	addWithRole(t, users, "maria@example.com", "Maria Perez", role.Author)
	cfg := serverConfig(users, newFakePostStore())
	cfg.Settings = newStoredSettings()
	cfg.Avatars = options
	return authedServerWithStores(t, cfg)
}

// listedAccounts returns the accounts GET /api/users answers, failing unless both accounts are listed.
func listedAccounts(t *testing.T, handler http.Handler) []listedAccount {
	t.Helper()
	recorder := doRequest(t, handler, http.MethodGet, "/api/users", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	listed := decodeBody[[]listedAccount](t, recorder)
	if len(listed) != 2 {
		t.Fatalf("listed %+v, want the administrator and Maria Perez", listed)
	}
	return listed
}

// sentAvatar returns the avatar the account carries, quoted, or "no avatar" when the key is missing.
func sentAvatar(account listedAccount) string {
	if account.Avatar == nil {
		return "no avatar"
	}
	return strconv.Quote(*account.Avatar)
}

// wantedAccountAvatar returns the avatar the users list must receive for the account under options.
func wantedAccountAvatar(account listedAccount, options avatar.Options) string {
	if options.Mode == avatar.ModeOff {
		return "no avatar"
	}
	return strconv.Quote(avatar.Address(account.Name, account.Email, usersListCell, options))
}

func TestUserListSendsEachAccountsAvatarAtTwiceThePhoneCell(t *testing.T) {
	t.Parallel()

	for name, options := range avatarCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := avatarUsersServer(t, options)

			listed := listedAccounts(t, handler)

			for _, account := range listed {
				if got, want := sentAvatar(account), wantedAccountAvatar(account, options); got != want {
					t.Errorf("avatar of %s = %s, want %s", account.Email, got, want)
				}
			}
		})
	}
}

func TestSettingsGetServesShowAvatarsFalseOnlyWhileAvatarsAreOff(t *testing.T) {
	t.Parallel()

	for name, options := range avatarCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler := avatarUsersServer(t, options)

			recorder := doRequest(t, handler, http.MethodGet, "/api/settings", "")

			var held struct {
				ShowAvatars *bool `json:"show_avatars"`
			}
			err := json.Unmarshal(recorder.Body.Bytes(), &held)
			want := options.Mode != avatar.ModeOff
			if err != nil || held.ShowAvatars == nil || *held.ShowAvatars != want {
				t.Errorf("GET /api/settings = %d %s, want show_avatars %v", recorder.Code, recorder.Body, want)
			}
		})
	}
}
